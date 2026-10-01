# Review queues

The existing unpaged endpoints remain compatible for addons.
The browser uses `paged=1&limit=50&after=...` on duplicate reports and Bin lists.
Pages return `total`, `next`, and only the requested window.
A cursor identifies the last item by a stable ordering key, so removing earlier items does not shift the next page.
Duplicate bulk actions apply only to the displayed page.
Bin pages include complete counts and bytes for each displayed item's action group, including members outside the page.
Confirmation therefore describes the whole batch that the writer will act on.
`summary=1` on the deletion report returns counts and no cards.

Library search uses a bounded GET endpoint with text, kind, status, favourite and capture-date filters.
Search is read-only and never opens original media on the request path.
Burst candidates use capture-time proximity within the same day folder and available perceptual evidence.
They are suggestions for explicit comparison, never proof of duplication or permission to remove another file automatically.

The OpenAPI response for `/api/trash` accepts the original array and the paged object.
Search cursors are tied to their filters; changing the query requires a fresh page.
Burst comparisons return at most 40 candidate files plus their stacked companions, capped at 400 files as complete stacks.
Companion decisions travel in the same existing atomic decision batch, with revision checks, a local recovery journal and undo.
The metadata worker reads at most 500 images per pass with four readers, yielding between full passes.
Five-minute sessions and up to eight named searches are saved in this browser.
A session pauses when its viewer closes or the document is hidden; it stops after 25 photographs or five active minutes.
