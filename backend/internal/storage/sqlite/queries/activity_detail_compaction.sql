-- The one-time activity detail backfill (B842). See
-- store.CompactActivityDetailsWindow and compactActivityDetail, whose rules the
-- two UPDATEs below repeat for rows written before it existed.
--
-- NOTE: keep these comments ASCII. sqlc locates its star-expansion edits by byte
-- offset, so a multi-byte character here silently corrupts later queries.

-- name: GetActivityDetailCompaction :one
SELECT compacted_through_rowid, through_rowid FROM activity_detail_compaction WHERE id = 1;

-- The rowid of the last_offset+1-th row above the cursor, or through_rowid when
-- fewer rows are left: the end of one window.
-- name: ActivityDetailCompactionWindowEnd :one
SELECT CAST(COALESCE((
    SELECT conversation_activities.rowid FROM conversation_activities
    WHERE conversation_activities.rowid > CAST(sqlc.arg(after_rowid) AS INTEGER)
      AND conversation_activities.rowid <= CAST(sqlc.arg(through_rowid) AS INTEGER)
    ORDER BY conversation_activities.rowid
    LIMIT 1 OFFSET CAST(sqlc.arg(last_offset) AS INTEGER)
), CAST(sqlc.arg(through_rowid) AS INTEGER)) AS INTEGER) AS window_end;

-- Output the read path replaces with the command_output column. instr() comes
-- first so a detail without the keys is never parsed.
-- name: CompactActivityDetailOutput :execrows
UPDATE conversation_activities
SET detail_json = json_remove(detail_json, '$.output', '$.outputTruncated', '$.outputSource', '$.outputMayBePartial')
WHERE conversation_activities.rowid > CAST(sqlc.arg(after_rowid) AS INTEGER)
  AND conversation_activities.rowid <= CAST(sqlc.arg(through_rowid) AS INTEGER)
  AND command_output <> ''
  AND instr(detail_json, '"output') > 0
  AND json_valid(detail_json)
  AND (json_type(detail_json, '$.output') IS NOT NULL
       OR json_type(detail_json, '$.outputTruncated') IS NOT NULL
       OR json_type(detail_json, '$.outputSource') IS NOT NULL
       OR json_type(detail_json, '$.outputMayBePartial') IS NOT NULL);

-- A rawCommand that only repeats command; every reader falls back to command.
-- name: CompactActivityDetailRawCommand :execrows
UPDATE conversation_activities
SET detail_json = json_remove(detail_json, '$.rawCommand')
WHERE conversation_activities.rowid > CAST(sqlc.arg(after_rowid) AS INTEGER)
  AND conversation_activities.rowid <= CAST(sqlc.arg(through_rowid) AS INTEGER)
  AND instr(detail_json, '"rawCommand"') > 0
  AND json_valid(detail_json)
  AND json_type(detail_json, '$.rawCommand') = 'text'
  AND json_type(detail_json, '$.command') = 'text'
  AND json_extract(detail_json, '$.rawCommand') = json_extract(detail_json, '$.command');

-- name: AdvanceActivityDetailCompaction :exec
UPDATE activity_detail_compaction
SET compacted_through_rowid = MAX(compacted_through_rowid, CAST(sqlc.arg(compacted_through_rowid) AS INTEGER)),
    updated_at = sqlc.arg(updated_at)
WHERE id = 1;
