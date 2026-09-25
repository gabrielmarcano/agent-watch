package com.gabriel.agentwatch.data

import com.gabriel.agentwatch.model.HistoryItem
import org.junit.Assert.assertEquals
import org.junit.Test

class HistoryMergeTest {

    /** Item n completed n minutes after 10:00 (so higher n = newer). */
    private fun item(n: Int) = HistoryItem(
        id = "h$n",
        pane_id = "w1:p1",
        response = "r$n",
        completed_at = "2026-09-25T%02d:%02d:00Z".format(10 + n / 60, n % 60)
    )

    @Test
    fun refreshKeepsWhatSseAccumulatedBeyondTheFetchedPage() {
        val fromSse = (60 downTo 11).map(::item)  // 50 items, newest first
        val fetched = (60 downTo 41).map(::item)  // the relay's latest 20, all already known

        val merged = mergeHistory(fromSse, fetched)

        assertEquals(50, merged.size)
        assertEquals((60 downTo 11).map { "h$it" }, merged.map { it.id })
    }

    @Test
    fun mergeDedupesByIdAndSortsNewestFirst() {
        val merged = mergeHistory(listOf(item(3), item(1)), listOf(item(4), item(3), item(2)))

        assertEquals(listOf("h4", "h3", "h2", "h1"), merged.map { it.id })
    }

    @Test
    fun anSseItemIsPrependedOnce() {
        val current = listOf(item(2), item(1))

        val once = mergeHistory(current, listOf(item(3)))
        val twice = mergeHistory(once, listOf(item(3)))

        assertEquals(listOf("h3", "h2", "h1"), twice.map { it.id })
    }

    @Test
    fun mergeIsCappedAtTheLimit() {
        val current = (300 downTo 101).map(::item)
        val merged = mergeHistory(current, (100 downTo 1).map(::item))

        assertEquals(HISTORY_LIMIT, merged.size)
        assertEquals("h300", merged.first().id)
    }
}
