// Package analytics implements privacy-safe reader event ingestion and reporting.
//
// The public ingestion endpoint accepts a batch with this shape:
//
//	{"events":[{"type":"page_view","page":1,"duration_ms":0,"session_id":"optional","metadata":{}}]}
//
// Public event types are view, qualified_read, page_view, active_time,
// furthest_page, completion, cta_click, outbound_click, download, search, and
// share. The legacy page_exposure name is accepted as an alias for page_view.
// Lead and purchase events are deliberately not accepted from public clients;
// lead and sales services must record those after their server-side operations.
//
// The following optional metadata fields are consumed. All other fields are
// discarded rather than persisted:
//   - event_id: an 8-128 character client event ID for retry deduplication.
//   - placement: "standalone" or "embed" (the default is standalone).
//   - embed_host: the embedding page's host, used for embed access checks.
//   - target_url (or url): an HTTP(S) link; only its host is retained.
//   - query (or search_terms): reader-entered search text, up to 200 runes.
//   - cta_id (or cta), link_id, channel, method, format: short safe labels.
//   - utm_source, utm_medium, utm_campaign, utm_content, utm_term.
//
// UTM values are also read from the ingestion URL and the referrer URL. The
// stored referrer never includes credentials, a query string, or a fragment.
// duration_ms represents foreground time and is retained as whole seconds on
// page_view and active_time events. Send a given interval on only one of those
// event types so it is not counted twice. page is required for page_view and
// furthest_page and is checked against the publication when its size is known.
//
// Session IDs supplied by a client and the server-issued session/reader cookies
// are only stored as keyed hashes. Raw IP addresses are never accepted or
// stored. Country is accepted only from configured proxy headers when country
// reporting is enabled. A reader opt-out or DNT=1 suppresses collection.
package analytics
