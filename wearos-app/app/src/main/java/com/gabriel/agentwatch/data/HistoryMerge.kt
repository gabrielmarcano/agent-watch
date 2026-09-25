package com.gabriel.agentwatch.data

import com.gabriel.agentwatch.model.HistoryItem

/** How many history items the watch keeps in memory. */
const val HISTORY_LIMIT = 200

/**
 * Unions [incoming] (an SSE `history` item, or a `GET /v1/history` page) into [current]: deduplicated by
 * `id` (deterministic per contracts §1.4), newest `completed_at` first, capped at [limit]. A fetched page
 * of 20 never discards older items that SSE already delivered.
 */
fun mergeHistory(
    current: List<HistoryItem>,
    incoming: List<HistoryItem>,
    limit: Int = HISTORY_LIMIT
): List<HistoryItem> =
    (incoming + current)
        .distinctBy { it.id }
        .sortedByDescending { it.completed_at } // RFC 3339 UTC strings sort chronologically; sort is stable
        .take(limit)
