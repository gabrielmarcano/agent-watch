package com.gabriel.agentwatch.ui.logic

import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant

class AgeTest {

    private val now = Instant.parse("2026-09-25T18:30:00Z")

    @Test
    fun underAMinuteIsJustNow() {
        assertEquals(Age.JustNow, ageOf("2026-09-25T18:29:15Z", now))
    }

    @Test
    fun minutesHoursAndDays() {
        assertEquals(Age.Minutes(5), ageOf("2026-09-25T18:25:00Z", now))
        assertEquals(Age.Minutes(59), ageOf("2026-09-25T17:30:30Z", now))
        assertEquals(Age.Hours(3), ageOf("2026-09-25T15:10:00Z", now))
        assertEquals(Age.Days(2), ageOf("2026-09-23T18:00:00Z", now))
    }

    @Test
    fun fractionalSecondsAndOffsetsParse() {
        assertEquals(Age.Minutes(5), ageOf("2026-09-25T18:24:59.123456789Z", now))
        assertEquals(Age.Minutes(5), ageOf("2026-09-25T14:25:00-04:00", now))
    }

    @Test
    fun aTimestampFromTheFutureIsJustNow() {
        // The Mac's clock can be ahead of the watch's.
        assertEquals(Age.JustNow, ageOf("2026-09-25T18:31:00Z", now))
    }

    @Test
    fun anUnreadableTimestampIsUnknown() {
        assertEquals(Age.Unknown, ageOf("", now))
        assertEquals(Age.Unknown, ageOf("yesterday", now))
    }
}
