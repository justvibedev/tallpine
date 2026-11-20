-- Provider-event archive repack. Run by the archive janitor in small batches,
-- never from the projection path.
--
-- Compaction and retention shrink rows to '{}' tombstones in place. A shrunk
-- row frees space inside its b-tree page, not a whole page, so incremental
-- vacuum cannot return it and the file keeps its high-water mark. Deleting a
-- window of consecutive rows and inserting the same rows again, in one
-- transaction, packs them into full pages and frees the rest, which
-- incremental vacuum then returns to the OS. No row changes: every column,
-- the id included, is written back as it was.
--
-- NOTE: keep these comments ASCII. sqlc locates its star-expansion edits by byte
-- offset, so a multi-byte character here silently corrupts later queries.

-- The ids above the cursor with their stored size, without reading a payload:
-- octet_length reads the stored length, not the value on its overflow pages.
-- The caller picks how many fit its byte budget.
-- name: SelectProviderEventSizesForRepack :many
SELECT e.id,
       CAST(octet_length(e.payload_json) AS INTEGER) AS payload_bytes
FROM conversation_provider_events e
WHERE e.id > sqlc.arg(after_id)
  AND e.id <= sqlc.arg(through_id)
ORDER BY e.id
LIMIT sqlc.arg(page_limit);
