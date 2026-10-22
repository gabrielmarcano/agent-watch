package com.gabriel.agentwatch.mobile.ui.logic

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.model.PendingPrompt
import com.gabriel.agentwatch.util.MdBlock
import com.gabriel.agentwatch.util.MdSpan
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.DayOfWeek
import java.time.Instant
import java.time.LocalDate
import java.time.LocalTime
import java.time.ZoneId
import java.time.ZoneOffset

class ChatEntriesTest {

    private val utc = ZoneOffset.UTC

    private fun turn(
        id: String,
        query: String? = null,
        response: String = "answer",
        completedAt: String,
        askedAt: String? = null,
        host: String = "h1",
        pane: String = "w1:p1",
        source: String = "transcript"
    ) = HistoryItem(
        id = id,
        pane_id = pane,
        agent = "claude",
        label = "Fix the build",
        query = query,
        response = response,
        source = source,
        asked_at = askedAt,
        completed_at = completedAt,
        host = host
    )

    private fun agent(
        status: String = "idle",
        seq: Long = 0,
        updatedAt: String = "2026-10-20T12:00:00Z",
        host: String = "h1",
        pane: String = "w1:p1"
    ) = AgentState(
        pane_id = pane,
        agent = "claude",
        label = "Fix the build",
        status = status,
        state_change_seq = seq,
        updated_at = updatedAt,
        host = host
    )

    @Test
    fun anEmptyConversationHasNothingToShow() {
        assertEquals(emptyList<ChatEntry>(), chatEntries(emptyList(), hasPrompt = false, working = false, zone = utc))
    }

    @Test
    fun turnsReadOldestFirstWithADaySeparatorOnlyWhenTheDayChanges() {
        val twoDays = listOf(
            turn("a", completedAt = "2026-10-22T09:00:00Z"),
            turn("b", completedAt = "2026-10-20T09:00:00Z")
        )
        assertEquals(
            listOf(
                ChatEntry.Day(LocalDate.of(2026, 10, 20)),
                ChatEntry.Turn(twoDays[1]),
                ChatEntry.Day(LocalDate.of(2026, 10, 22)),
                ChatEntry.Turn(twoDays[0])
            ),
            chatEntries(twoDays, hasPrompt = false, working = false, zone = utc)
        )

        val sameDay = listOf(
            turn("a", completedAt = "2026-10-20T11:00:00Z"),
            turn("b", completedAt = "2026-10-20T09:00:00Z")
        )
        assertEquals(
            listOf(
                ChatEntry.Day(LocalDate.of(2026, 10, 20)),
                ChatEntry.Turn(sameDay[1]),
                ChatEntry.Turn(sameDay[0])
            ),
            chatEntries(sameDay, hasPrompt = false, working = false, zone = utc)
        )
    }

    @Test
    fun aTurnTakesTheDayItWasAskedEvenWhenTheAnswerCameTheNextDay() {
        val item = turn(
            "a",
            completedAt = "2026-10-21T00:30:00Z",
            askedAt = "2026-10-20T23:59:00Z"
        )
        assertEquals(
            listOf(
                ChatEntry.Day(LocalDate.of(2026, 10, 20)),
                ChatEntry.Turn(item)
            ),
            chatEntries(listOf(item), hasPrompt = false, working = false, zone = utc)
        )
    }

    @Test
    fun aTurnWithoutAParseableDateStillAppearsWithoutASeparator() {
        val item = turn("a", completedAt = "not a time")
        assertEquals(
            listOf(ChatEntry.Turn(item)),
            chatEntries(listOf(item), hasPrompt = false, working = false, zone = utc)
        )
    }

    @Test
    fun pendingMessagesFollowTheTurnsAndBlankOnesAreDropped() {
        val item = turn("a", completedAt = "2026-10-20T09:00:00Z")
        assertEquals(
            listOf(
                ChatEntry.Day(LocalDate.of(2026, 10, 20)),
                ChatEntry.Turn(item),
                ChatEntry.Pending("run the tests"),
                ChatEntry.Pending("then commit")
            ),
            chatEntries(
                history = listOf(item),
                hasPrompt = false,
                working = false,
                pending = listOf("run the tests", " ", "then commit"),
                zone = utc
            )
        )
    }

    @Test
    fun aPromptComesAfterEverythingAndWinsOverWorking() {
        val item = turn("a", completedAt = "2026-10-20T09:00:00Z")
        assertEquals(
            listOf(
                ChatEntry.Day(LocalDate.of(2026, 10, 20)),
                ChatEntry.Turn(item),
                ChatEntry.Prompt
            ),
            chatEntries(listOf(item), hasPrompt = true, working = true, zone = utc)
        )
        assertEquals(
            listOf(
                ChatEntry.Day(LocalDate.of(2026, 10, 20)),
                ChatEntry.Turn(item),
                ChatEntry.Working
            ),
            chatEntries(listOf(item), hasPrompt = false, working = true, zone = utc)
        )
    }

    @Test
    fun aTurnKeepsItsOwnDayAtThePhoneTimeZone() {
        // 23:30 UTC on the 20th is already the 21st in Madrid (UTC+2 in October).
        val item = turn("a", completedAt = "2026-10-20T23:30:00Z")
        assertEquals(
            listOf(
                ChatEntry.Day(LocalDate.of(2026, 10, 21)),
                ChatEntry.Turn(item)
            ),
            chatEntries(listOf(item), hasPrompt = false, working = false, zone = ZoneId.of("Europe/Madrid"))
        )
    }

    @Test
    fun aPendingMessageDisappearsWhenItsTurnArrives() {
        val history = listOf(
            turn("b", query = "run the tests", completedAt = "2026-10-20T09:00:00Z"),
            turn("a", query = "fix it", completedAt = "2026-10-20T10:00:00Z")
        )
        assertEquals(
            listOf("then commit"),
            unmatchedPending(history, listOf("run the tests", "then commit"))
        )
        assertEquals(emptyList<String>(), unmatchedPending(history, emptyList()))
        assertEquals(listOf("new"), unmatchedPending(emptyList(), listOf("new")))
    }

    @Test
    fun theChatListSortsByTheNewestTurnOfEachChat() {
        val mine = agent(updatedAt = "2026-10-20T12:00:00Z")
        val other = agent(pane = "w1:p2", updatedAt = "2026-10-20T13:00:00Z")
        val history = listOf(
            turn("c", completedAt = "2026-10-20T14:00:00Z", pane = "w1:p2"),
            turn("b", completedAt = "2026-10-20T11:00:00Z"),
            turn("a", completedAt = "2026-10-20T10:00:00Z")
        )
        assertEquals(Instant.parse("2026-10-20T11:00:00Z"), mine.lastActivity(history))
        assertEquals(Instant.parse("2026-10-20T14:00:00Z"), other.lastActivity(history))
        assertEquals(Instant.parse("2026-10-20T12:00:00Z"), mine.lastActivity(emptyList()))
        assertEquals(Instant.EPOCH, mine.copy(updated_at = "nope").lastActivity(emptyList()))
    }

    @Test
    fun aChatIsUnreadOnlyAfterASeenSequence() {
        val blocked = agent(status = "blocked", seq = 7)
        assertFalse(isUnread(blocked, seenSeq = null))
        assertFalse(isUnread(blocked, seenSeq = 7))
        assertTrue(isUnread(blocked, seenSeq = 6))
        assertFalse(isUnread(agent(seq = 0), seenSeq = 0))
    }

    @Test
    fun aChatNeedsYouWhenAPromptWaitsOrHerdrSaysBlocked() {
        assertTrue(agent(status = "blocked").needsAnswer())
        assertTrue(agent(status = "working").copy(prompt = PendingPrompt()).needsAnswer())
        assertFalse(agent(status = "done").needsAnswer())
        assertFalse(agent(status = "working").needsAnswer())
    }

    @Test
    fun thePreviewIsTheFirstLineOfTheCleanAnswer() {
        val answer = turn(
            id = "a",
            response = "# Fixed\n\n**The build** passes now.\nSecond line.",
            completedAt = "2026-10-20T09:00:00Z"
        )
        assertEquals("Fixed", previewLine(answer))
        assertNull(previewLine(answer.copy(response = "  \n\n ")))
        assertEquals(
            "Fixed",
            previewLine(answer.copy(response = "\n\n# Fixed")),
        )
    }

    @Test
    fun aLongPreviewIsCutAtTheLimit() {
        val answer = turn(id = "a", response = "x".repeat(400), completedAt = "2026-10-20T09:00:00Z")
        val preview = previewLine(answer, maxChars = 20)!!
        assertEquals(20, preview.length)
        assertTrue(preview.endsWith("…"))
    }

    @Test
    fun aTimestampIsTheHourTodayTheDayThisWeekAndADateAfterThat() {
        val now = Instant.parse("2026-10-20T12:00:00Z")

        assertEquals(
            ChatStamp.Today(LocalTime.of(9, 5)),
            chatStamp("2026-10-20T09:05:00Z", now, utc)
        )
        assertEquals(
            ChatStamp.ThisWeek(DayOfWeek.SUNDAY),
            chatStamp("2026-10-18T09:05:00Z", now, utc)
        )
        assertEquals(
            ChatStamp.Older(LocalDate.of(2026, 10, 1)),
            chatStamp("2026-10-01T09:05:00Z", now, utc)
        )
        assertEquals(null, chatStamp("", now, utc))
        // The host's clock ahead of the phone's: still today, never "in the future".
        assertEquals(
            ChatStamp.Today(LocalTime.of(13, 0)),
            chatStamp("2026-10-20T13:00:00Z", now, utc)
        )
    }

    @Test
    fun aDaySeparatorSaysTodayYesterdayOrTheDate() {
        val today = LocalDate.of(2026, 10, 20)
        assertEquals(ChatDay.Today, chatDay(LocalDate.of(2026, 10, 20), today))
        assertEquals(ChatDay.Yesterday, chatDay(LocalDate.of(2026, 10, 19), today))
        assertEquals(ChatDay.Older(LocalDate.of(2026, 10, 18)), chatDay(LocalDate.of(2026, 10, 18), today))
    }

    @Test
    fun theFiltersKeepTheChatsTheyName() {
        val waiting = agent(status = "blocked")
        val working = agent(status = "working", pane = "w1:p2")
        val done = agent(status = "done", pane = "w1:p3")
        val idle = agent(status = "idle", pane = "w1:p4")

        assertTrue(ChatFilter.ALL.matches(waiting))
        assertTrue(ChatFilter.NEEDS_YOU.matches(waiting))
        assertFalse(ChatFilter.NEEDS_YOU.matches(working))
        assertTrue(ChatFilter.WORKING.matches(working))
        assertTrue(ChatFilter.DONE.matches(done))
        assertFalse(ChatFilter.DONE.matches(idle))
    }

    @Test
    fun theUnreadBadgeCountsTheTurnsAfterTheChatWasRead() {
        val read = Instant.parse("2026-10-20T10:00:00Z").toEpochMilli()
        val history = listOf(
            turn(id = "old", completedAt = "2026-10-20T09:00:00Z"),
            turn(id = "new1", completedAt = "2026-10-20T10:30:00Z"),
            turn(id = "new2", completedAt = "2026-10-20T11:00:00Z")
        )

        assertEquals(2, unreadCount(agent(), history, read))
        // Never read (the list primes it): no badge.
        assertEquals(0, unreadCount(agent(), history, null))
        // Another pane's turn is not this chat's.
        assertEquals(
            0,
            unreadCount(agent(pane = "w1:p9"), history, read)
        )
    }

    @Test
    fun aLongAnswerIsShownAsAPartWithReadMore() {
        val blocks = listOf(
            MdBlock.Paragraph(listOf(MdSpan("a".repeat(200)))),
            MdBlock.Paragraph(listOf(MdSpan("b".repeat(200)))),
            MdBlock.Paragraph(listOf(MdSpan("c".repeat(200))))
        )

        val short = truncateBlocks(blocks, limit = 240)
        assertTrue(short.truncated)
        assertEquals(1, short.blocks.size)

        val whole = truncateBlocks(blocks, limit = 1000)
        assertFalse(whole.truncated)
        assertEquals(3, whole.blocks.size)

        // The first block always shows, even when it is longer than the limit.
        val one = truncateBlocks(blocks.take(1), limit = 10)
        assertEquals(1, one.blocks.size)
        assertFalse(one.truncated)
    }

    @Test
    fun theSearchMatchesTheNameTheAgentTheMachineAndTheLastMessage() {
        val history = listOf(turn(id = "a", response = "the relay is up", completedAt = "2026-10-20T09:00:00Z"))
        val chat = agent()

        assertTrue(matchesQuery(chat, history, "fix"))
        assertTrue(matchesQuery(chat, history, "claude"))
        assertTrue(matchesQuery(chat, history, "relay"))
        assertTrue(matchesQuery(chat, history, ""))
        assertFalse(matchesQuery(chat, history, "nothing here"))
    }
}
