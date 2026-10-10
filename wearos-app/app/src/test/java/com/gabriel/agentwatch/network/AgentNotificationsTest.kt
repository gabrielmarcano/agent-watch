package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class AgentNotificationsTest {

    private fun blockedData(pane: String = "w5:pAE", seq: String = "334") = mapOf(
        "event" to "blocked", "pane_id" to pane, "agent" to "claude", "label" to "my-app",
        "title" to "my-app needs approval", "body" to "Bash: go test ./...", "state_change_seq" to seq,
        "fingerprint" to "9f2c61d0a4b3e871", "allow_option_id" to "opt-1", "deny_option_id" to "opt-3"
    )

    // ---- item 7: parsing, including the new data-only "resolved" event

    @Test
    fun parsesABlockedPush() {
        val msg = PushMessage.parse(blockedData()) as PushMessage.Blocked
        assertEquals("w5:pAE", msg.paneId)
        assertEquals(334L, msg.seq)
        assertEquals("9f2c61d0a4b3e871", msg.fingerprint)
        assertEquals("opt-1", msg.allowOptionId)
        assertEquals("opt-3", msg.denyOptionId)
        assertEquals("my-app needs approval", msg.title)
    }

    @Test
    fun parsesAResolvedPushAsDataOnly() {
        val msg = PushMessage.parse(mapOf("event" to "resolved", "pane_id" to "w5:pAE", "state_change_seq" to "335"))
        assertEquals(PushMessage.Resolved("w5:pAE", 335L), msg)
    }

    @Test
    fun resolvedWithoutSeqStillParses() {
        assertEquals(PushMessage.Resolved("w5:pAE", null), PushMessage.parse(mapOf("event" to "resolved", "pane_id" to "w5:pAE")))
    }

    @Test
    fun unknownOrIncompleteEventsShowNothing() {
        assertTrue(PushMessage.parse(emptyMap()) is PushMessage.Ignored)
        assertTrue(PushMessage.parse(mapOf("event" to "something_new", "pane_id" to "p")) is PushMessage.Ignored)
        assertTrue(PushMessage.parse(mapOf("event" to "resolved")) is PushMessage.Ignored)
        assertTrue(PushMessage.parse(blockedData(pane = "")) is PushMessage.Ignored)
    }

    @Test
    fun doneAndDigestParse() {
        assertTrue(PushMessage.parse(mapOf("event" to "done", "pane_id" to "p", "label" to "x")) is PushMessage.Done)
        assertTrue(PushMessage.parse(mapOf("event" to "digest", "title" to "3 agents need you")) is PushMessage.Digest)
    }

    @Test
    fun logSummaryNeverContainsThePromptText() {
        val summary = PushMessage.parse(blockedData()).logSummary()
        assertFalse(summary.contains("go test"))
        assertFalse(summary.contains("needs approval"))
        assertTrue(summary.contains("w5:pAE"))
    }

    @Test
    fun resolvedTargetsTheSameNotificationIdAsTheBlockedOne() {
        val blocked = PushMessage.parse(blockedData()) as PushMessage.Blocked
        val resolved = PushMessage.parse(mapOf("event" to "resolved", "pane_id" to "w5:pAE", "state_change_seq" to "335")) as PushMessage.Resolved
        assertEquals(AgentNotifications.idFor(blocked.key), AgentNotifications.idFor(resolved.key))
        assertTrue(AgentNotifications.idFor(AgentKey("", "w5:pAE")) >= 0)
    }

    @Test
    fun resolvedDismissesTheShownApprovalUnlessItIsNewer() {
        val shown = ShownApproval(notificationId = 7, paneId = "w5:pAE", seq = 334)
        assertTrue(shouldDismissOnResolved(shown, resolvedSeq = 335))
        assertTrue(shouldDismissOnResolved(shown, resolvedSeq = 334))
        assertTrue("no seq: trust the relay", shouldDismissOnResolved(shown, resolvedSeq = null))
        assertFalse("a newer prompt arrived before this late 'resolved'", shouldDismissOnResolved(shown, resolvedSeq = 333))
        assertFalse(shouldDismissOnResolved(null, resolvedSeq = 335))
    }

    @Test
    fun repositoryNewsDismissesApprovalsForAgentsNoLongerBlocked() {
        val shown = listOf(
            ShownApproval(1, "A", 10), // A moved on: working at 11
            ShownApproval(2, "B", 10), // B still blocked at 10
            ShownApproval(3, "C", 10), // C gone
            ShownApproval(4, "D", 10)  // D's list entry is older than the notification
        )
        val agents = listOf(
            AgentState(pane_id = "A", status = "working", state_change_seq = 11),
            AgentState(pane_id = "B", status = "blocked", state_change_seq = 10),
            AgentState(pane_id = "D", status = "working", state_change_seq = 9)
        )

        assertEquals(listOf(1, 3), approvalsToDismiss(shown, AgentsUpdate.All(agents)))
        assertEquals(listOf(1), approvalsToDismiss(shown, AgentsUpdate.Changed(agents[0])))
        assertEquals(emptyList<Int>(), approvalsToDismiss(shown, AgentsUpdate.Changed(agents[1])))
        assertEquals(listOf(2), approvalsToDismiss(shown, AgentsUpdate.Removed("B")))
    }

    // ---- item 9: PendingIntent identity per (pane, action)

    @Test
    fun theOldRequestCodesCollideButTheNewIntentIdentitiesDoNot() {
        val (a, b) = collidingPanesUnderTheOldScheme()
        assertEquals("precondition: old codes collide", oldRequestCode(a), oldRequestCode(b))

        for (action in NotificationAction.values()) {
            assertNotEquals(AgentNotifications.intentUri(AgentKey("", a), action), AgentNotifications.intentUri(AgentKey("", b), action))
        }
    }

    @Test
    fun intentIdentityIsStablePerPaneAndDistinctPerAction() {
        assertEquals(
            AgentNotifications.intentUri(AgentKey("", "w5:pAE"), NotificationAction.ALLOW),
            AgentNotifications.intentUri(AgentKey("", "w5:pAE"), NotificationAction.ALLOW)
        )
        val uris = NotificationAction.values().map { AgentNotifications.intentUri(AgentKey("", "w5:pAE"), it) }
        assertEquals(uris.size, uris.toSet().size)
        // pane ids are opaque: "a/b" must not look like pane "a" + something
        assertNotEquals(
            AgentNotifications.intentUri(AgentKey("", "a/allow"), NotificationAction.OPEN),
            AgentNotifications.intentUri(AgentKey("", "a"), NotificationAction.ALLOW)
        )
    }

    @Test
    fun requestCodesDifferPerActionForOnePane() {
        val codes = NotificationAction.values().map { AgentNotifications.requestCode(AgentKey("", "w5:pAE"), it) }
        assertEquals(codes.size, codes.toSet().size)
    }

    private fun oldRequestCode(pane: String) = ((pane.hashCode() and 0x7FFFFFFF) % 100000) * 10 + 1

    private fun collidingPanesUnderTheOldScheme(): Pair<String, String> {
        val seen = HashMap<Int, String>()
        for (n in 0..1_000_000) {
            val pane = "w$n:pAE"
            val code = oldRequestCode(pane)
            seen[code]?.let { return it to pane }
            seen[code] = pane
        }
        error("no collision found")
    }

    // ---- item 10: feedback wording

    @Test
    fun cancelFeedbackSaysCanceledNotDenied() {
        assertEquals("Canceled", actionSuccessTitle(NotificationActionReceiver.ACTION_CANCEL, isDeny = false))
        assertEquals("Denied", actionSuccessTitle(NotificationActionReceiver.ACTION_ANSWER, isDeny = true))
        assertEquals("Approved", actionSuccessTitle(NotificationActionReceiver.ACTION_ANSWER, isDeny = false))
        assertEquals("Sent", actionSuccessTitle(NotificationActionReceiver.ACTION_PROMPT, isDeny = false))
    }

    // ---- one-tap answers (contracts §4.1 kind / options)

    private fun blocked(extra: Map<String, String>) = PushMessage.parse(blockedData() + extra) as PushMessage.Blocked

    @Test
    fun aQuestionOffersItsAnswersAndNoCancel() {
        val msg = blocked(
            mapOf(
                "kind" to "question", "allow_option_id" to "", "deny_option_id" to "",
                "options" to """[{"id":"opt-1","label":"Rojo"},{"id":"opt-2","label":"Verde"}]"""
            )
        )
        assertEquals(listOf(PushChoice("opt-1", "Rojo"), PushChoice("opt-2", "Verde")), msg.options)
        assertEquals(
            listOf(
                NotificationButton(NotificationButton.Kind.ANSWER, "Rojo", "opt-1"),
                NotificationButton(NotificationButton.Kind.ANSWER, "Verde", "opt-2"),
                NotificationButton(NotificationButton.Kind.OPEN, "Open")
            ),
            blockedButtons(msg)
        )
    }

    @Test
    fun aPermissionOffersAllowDenyAndOpen() {
        val buttons = blockedButtons(blocked(mapOf("kind" to "permission", "options" to "")))
        assertEquals(
            listOf(
                NotificationButton(NotificationButton.Kind.ANSWER, "Allow", "opt-1"),
                NotificationButton(NotificationButton.Kind.DENY, "Deny", "opt-3"),
                NotificationButton(NotificationButton.Kind.OPEN, "Open")
            ),
            buttons
        )
    }

    @Test
    fun anUnknownPromptOnlyOpensTheApp() {
        val buttons = blockedButtons(blocked(mapOf("kind" to "unknown", "allow_option_id" to "", "deny_option_id" to "")))
        assertEquals(listOf(NotificationButton(NotificationButton.Kind.OPEN, "Open")), buttons)
    }

    @Test
    fun anOldRelayWithoutKindKeepsTheOldButtons() {
        // No kind: allow when there is an allow option; deny, or cancel when there is no deny option.
        val noDeny = blocked(mapOf("deny_option_id" to ""))
        assertEquals(
            listOf(
                NotificationButton(NotificationButton.Kind.ANSWER, "Allow", "opt-1"),
                NotificationButton(NotificationButton.Kind.CANCEL, "Cancel"),
                NotificationButton(NotificationButton.Kind.OPEN, "Open")
            ),
            blockedButtons(noDeny)
        )
    }

    @Test
    fun malformedOptionsAreIgnored() {
        assertEquals(emptyList<PushChoice>(), blocked(mapOf("kind" to "question", "options" to "[{oops")).options)
    }

    // ---- phase 8: (host, pane) is the key

    @Test
    fun pushesCarryTheHost() {
        val blocked = PushMessage.parse(blockedData() + mapOf("host" to "box", "host_name" to "Box", "title" to "Box · my-app needs approval")) as PushMessage.Blocked
        assertEquals(AgentKey("box", "w5:pAE"), blocked.key)
        assertEquals("Box", blocked.hostName)
        assertEquals("the relay's title is shown as it is, never prefixed again", "Box · my-app needs approval", blocked.title)
        val done = PushMessage.parse(mapOf("event" to "done", "pane_id" to "p", "host" to "box", "host_name" to "Box")) as PushMessage.Done
        assertEquals(AgentKey("box", "p"), done.key)
        assertEquals(
            PushMessage.Resolved("w5:pAE", 335L, host = "box"),
            PushMessage.parse(mapOf("event" to "resolved", "host" to "box", "pane_id" to "w5:pAE", "state_change_seq" to "335"))
        )
        assertEquals("an older relay sends no host", "", (PushMessage.parse(blockedData()) as PushMessage.Blocked).host)
    }

    @Test
    fun twoHostsSharingAPaneGetTwoNotifications() {
        val mac = AgentKey("main", "w1:p1")
        val box = AgentKey("box", "w1:p1")
        assertNotEquals(AgentNotifications.idFor(mac), AgentNotifications.idFor(box))
        assertTrue(AgentNotifications.idFor(box) >= 0)
        assertEquals("without a host the id is the pane's, as before hosts", "w1:p1".hashCode() and 0x7FFFFFFF, AgentNotifications.idFor(AgentKey("", "w1:p1")))
        for (action in NotificationAction.values()) {
            assertNotEquals(AgentNotifications.intentUri(mac, action), AgentNotifications.intentUri(box, action))
            assertNotEquals(AgentNotifications.intentUri(box, action), AgentNotifications.intentUri(AgentKey("", "w1:p1"), action))
        }
        assertEquals("agentwatch://notification/open/w1%3Ap1?host=box", AgentNotifications.intentUri(box, NotificationAction.OPEN))
        assertEquals("agentwatch://notification/open/w1%3Ap1", AgentNotifications.intentUri(AgentKey("", "w1:p1"), NotificationAction.OPEN))
    }

    @Test
    fun repositoryNewsDismissesOnlyTheApprovalOfItsHost() {
        val shown = listOf(
            ShownApproval(1, "w1:p1", 10, host = "main"),
            ShownApproval(2, "w1:p1", 10, host = "box")
        )
        val macMovedOn = AgentState(pane_id = "w1:p1", host = "main", status = "working", state_change_seq = 11)
        val boxBlocked = AgentState(pane_id = "w1:p1", host = "box", status = "blocked", state_change_seq = 10)

        assertEquals(listOf(1), approvalsToDismiss(shown, AgentsUpdate.All(listOf(macMovedOn, boxBlocked))))
        assertEquals(listOf(1), approvalsToDismiss(shown, AgentsUpdate.Changed(macMovedOn)))
        assertEquals(listOf(2), approvalsToDismiss(shown, AgentsUpdate.Removed("w1:p1", host = "box")))
    }
}
