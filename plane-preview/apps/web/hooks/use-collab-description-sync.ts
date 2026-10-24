import type { EditorRefApi } from "@plane/editor";
import { convertBinaryDataToBase64String } from "@plane/editor";
import type { TDocumentPayload } from "@plane/types";
import { useEffect, useRef } from "react";

/**
 * Persists the page's document payload (`description_html`, `description_json`,
 * `description_binary`) while the editor runs in collaboration mode.
 *
 * Why this exists: the collaboration server (`internal/live`, see
 * `docs/10-REMAINING-WORK.md` §3.3) relays yjs updates without decoding them, so
 * it cannot derive HTML the way `apps/live` does when it stores a document.
 * Everything downstream of `description_html` — publishing a page to a Space,
 * full-text search, `description_stripped` — would otherwise stay frozen at
 * whatever the page contained before this deployment.
 *
 * `description_binary` is the same document encoded as a yjs state update. The
 * relay uses it as a fallback: if a collaboration log is missing (restored
 * database, wiped data directory) the page would otherwise open blank and the
 * next autosave would overwrite the stored HTML with nothing.
 *
 * The write is debounced and compares HTML before sending, so a burst of
 * keystrokes produces one PATCH and an idle page produces none.
 */
const DEBOUNCE_MS = 1500;
const ATTACH_INTERVAL_MS = 300;
// Replay of the relayed document does not emit `onStateChange` (it is a remote
// yjs update, not a local edit), so the write-back also runs once after the
// editor settles. 500ms × 20 gives a slow replay ~10s before we give up.
const SETTLE_INTERVAL_MS = 500;
const SETTLE_MAX_TICKS = 20;

type TArgs = {
  editorRef: React.RefObject<EditorRefApi | null>;
  pageId: string;
  /** `page.description_html` as loaded from the API; `undefined` while hydrating. */
  initialHtml?: string;
  updatePageDescription: (document: TDocumentPayload) => Promise<void>;
};

const stripTags = (html: string) => html.replace(/<[^>]*>/g, "").trim();

export const useCollabDescriptionSync = ({ editorRef, pageId, initialHtml, updatePageDescription }: TArgs) => {
  // Last HTML that reached the server; `null` means "unknown, save next time".
  const lastSavedHtml = useRef<string | null>(null);
  const isSaving = useRef(false);
  // Kept in a ref, not in the dependency list: the effect must run once per
  // page, and the baseline arrives later than the editor does.
  const baselineHtml = useRef<string | undefined>(initialHtml);
  baselineHtml.current = initialHtml;

  useEffect(() => {
    let stopListening: (() => void) | undefined;
    let debounceTimer: number | undefined;
    let attachTimer: number | undefined;
    let settleTimer: number | undefined;
    let disposed = false;

    /**
     * @param allowEmpty write an empty document over a non-empty one. Only a
     * real edit may do that (the user selected all and deleted); the automatic
     * settle pass must not, or a page whose relayed document failed to arrive
     * would be blanked on open.
     */
    const flush = async (allowEmpty: boolean) => {
      const editor = editorRef.current;
      if (!editor || isSaving.current) return;

      const { binary, html, json } = editor.getDocument();
      if (!html || html === lastSavedHtml.current) return;
      if (!allowEmpty && stripTags(html).length === 0 && (baselineHtml.current ?? "") !== "") {
        // Nothing came back from the relay although the server has content.
        // Keep the server copy: an empty PATCH here is data loss, not a delete.
        console.warn("[pages] relayed document is empty but the server has content; not overwriting");
        return;
      }

      lastSavedHtml.current = html;
      isSaving.current = true;
      try {
        await updatePageDescription({
          description_html: html,
          description_json: json ?? {},
          // The editor returns the yjs state update; the API stores it as a
          // protected file. Empty means the provider is not attached, in which
          // case there is nothing meaningful to send.
          description_binary: binary ? convertBinaryDataToBase64String(binary) : "",
        });
      } catch (error) {
        // A locked or archived page rejects the PATCH. Forget the optimistic
        // value so the next edit retries instead of pretending it was stored.
        lastSavedHtml.current = null;
        console.error("[pages] could not persist description_html", error);
      } finally {
        isSaving.current = false;
      }
    };

    const schedule = () => {
      window.clearTimeout(debounceTimer);
      debounceTimer = window.setTimeout(() => void flush(true), DEBOUNCE_MS);
    };

    // The editor (and its yjs document) is created asynchronously, so the ref
    // is still empty on mount and there is no event to subscribe to yet.
    const attach = () => {
      const editor = editorRef.current;
      if (!editor) return false;
      stopListening = editor.onStateChange(schedule);
      return true;
    };

    // One write-back after the relayed document settles. Without this a page
    // whose content only lives in the collaboration log (edited before this
    // hook shipped, or in a session whose PATCH failed) keeps an empty
    // `description_html` and publishes as a blank page to its Space.
    //
    // The baseline comes from the page store, which collapses an empty
    // `description_html` to `undefined` (`base-page.ts`). So:
    //   - baseline known  → write when the document differs from it;
    //   - baseline unknown → the server had no HTML, write if the document
    //     has any text at all.
    // A page that is empty on both sides produces neither, so merely opening
    // a page never PATCHes it.
    const settle = () => {
      let ticks = 0;
      const tick = () => {
        if (disposed) return;
        const editor = editorRef.current;
        if (editor) {
          const { html } = editor.getDocument();
          const baseline = baselineHtml.current;
          const hasText = stripTags(html).length > 0;
          if (baseline !== undefined) {
            if (html === baseline) {
              // Already in sync; remember it so the next keystroke is a diff.
              lastSavedHtml.current = html;
              return;
            }
            if (hasText) {
              void flush(false);
              return;
            }
          } else if (hasText) {
            void flush(false);
            return;
          }
        }
        ticks += 1;
        if (ticks >= SETTLE_MAX_TICKS) return;
        settleTimer = window.setTimeout(tick, SETTLE_INTERVAL_MS);
      };
      settleTimer = window.setTimeout(tick, 800);
    };

    if (!attach()) {
      attachTimer = window.setInterval(() => {
        if (attach() && attachTimer !== undefined) {
          window.clearInterval(attachTimer);
          settle();
        }
      }, ATTACH_INTERVAL_MS);
    } else {
      settle();
    }

    return () => {
      disposed = true;
      window.clearTimeout(debounceTimer);
      window.clearTimeout(settleTimer);
      if (attachTimer !== undefined) window.clearInterval(attachTimer);
      stopListening?.();
      // Leaving the page mid-edit: one last attempt so the change is not lost.
      void flush(true);
    };
  }, [editorRef, pageId, updatePageDescription]);
};
