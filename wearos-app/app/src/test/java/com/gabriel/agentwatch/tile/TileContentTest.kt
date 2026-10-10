package com.gabriel.agentwatch.tile

import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.AgentKey
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.IOException

class TileContentTest {

    private val idle = AgentState(pane_id = "w1:pA", label = "A", status = "idle", focused = true)
    private val blocked1 = AgentState(pane_id = "w1:pB", label = "B", status = "blocked")
    private val blocked2 = AgentState(pane_id = "w1:pC", label = "C", status = "blocked")

    private fun snapshot(vararg agents: AgentState, host: Boolean = true) =
        Result.success(AgentsSnapshot(host_online = host, herdr_online = true, agents = agents.toList()))

    @Test
    fun notPaired() {
        assertEquals(TileContent.NotPaired, tileContent(paired = false, snapshot = null, pinned = null))
    }

    @Test
    fun aFailedFetchSaysSo() {
        assertEquals(TileContent.Unreachable, tileContent(true, Result.failure(IOException("x")), null))
        assertEquals(TileContent.Unreachable, tileContent(true, null, null))
    }

    @Test
    fun theTargetComesWithHowManyOthersNeedTheUser() {
        val content = tileContent(true, snapshot(idle, blocked1, blocked2), pinned = AgentKey("", "w1:pA"))
        assertEquals(TileContent.Target(idle, othersNeedingYou = 2, macOnline = true), content)
    }

    @Test
    fun aBlockedTargetDoesNotCountItself() {
        val content = tileContent(true, snapshot(idle, blocked1, blocked2), pinned = AgentKey("", "w1:pB"))
        assertEquals(TileContent.Target(blocked1, othersNeedingYou = 1, macOnline = true), content)
    }

    @Test
    fun macOfflineIsCarried() {
        val content = tileContent(true, snapshot(idle, host = false), pinned = null)
        assertEquals(TileContent.Target(idle, othersNeedingYou = 0, macOnline = false), content)
    }

    @Test
    fun noTargetStillCountsWhoNeedsTheUser() {
        val unfocused = idle.copy(focused = false)
        assertEquals(TileContent.NoTarget(needingYou = 0), tileContent(true, snapshot(unfocused), null))
        assertEquals(TileContent.NoTarget(needingYou = 0), tileContent(true, snapshot(), null))
    }

    // ---- phase 8: across hosts

    private val box = HostInfo(id = "box", name = "Box", online = true, herdr_online = true)
    private val mac = HostInfo(id = "main", name = "Mac", online = true, herdr_online = true)

    @Test
    fun thePinnedAgentIsTheOneOnItsHostWithItsName() {
        val onMac = idle.copy(host = "main", focused = false)
        val onBox = idle.copy(host = "box", focused = false)
        val blockedOnMac = blocked1.copy(host = "main")
        val snapshot = Result.success(AgentsSnapshot(
            host_online = true, herdr_online = true, hosts = listOf(box, mac.copy(online = false)),
            agents = listOf(onMac, onBox, blockedOnMac)
        ))

        val content = tileContent(true, snapshot, pinned = AgentKey("main", "w1:pA"))

        assertEquals(TileContent.Target(onMac, othersNeedingYou = 1, macOnline = false, hostName = "Mac"), content)
        assertEquals(
            TileContent.Target(onBox, othersNeedingYou = 1, macOnline = true, hostName = "Box"),
            tileContent(true, snapshot, pinned = AgentKey("box", "w1:pA"))
        )
    }

    @Test
    fun oneHostIsNotNamed() {
        val onMac = idle.copy(host = "main")
        val content = tileContent(true, Result.success(AgentsSnapshot(host_online = true, hosts = listOf(mac), agents = listOf(onMac))), null)
        assertEquals(TileContent.Target(onMac, othersNeedingYou = 0, macOnline = true, hostName = null), content)
    }
}
