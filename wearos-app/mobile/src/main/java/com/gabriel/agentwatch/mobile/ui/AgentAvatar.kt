package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.theme.OnSurface
import com.gabriel.agentwatch.mobile.ui.theme.SurfaceHigh
import com.gabriel.agentwatch.mobile.ui.theme.statusStyle
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.ui.logic.agentBrand
import com.gabriel.agentwatch.ui.logic.shownStatus

/**
 * A chat's face: the agent type's mark (ARCHITECTURE.md §4b, `agentBrand`) in a circle, with the
 * agent's status as a dot. The dot never carries the status alone: the chat list repeats the status
 * word next to it.
 */
@Composable
fun AgentAvatar(
    agent: AgentState,
    size: Dp = 48.dp,
    ring: Color = MaterialTheme.colorScheme.background
) {
    val brand = remember(agent.agent) { agentBrand(agent.agent) }
    val style = statusStyle(agent.shownStatus())
    val name = brand.name.ifBlank { stringResource(R.string.agent_avatar) }
    val dot = size / 4

    Box(Modifier.size(size)) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .clip(CircleShape)
                .background(SurfaceHigh),
            contentAlignment = Alignment.Center
        ) {
            Icon(
                painter = painterResource(brand.icon),
                contentDescription = name,
                tint = OnSurface,
                modifier = Modifier.size(size * 0.62f)
            )
        }
        Box(
            modifier = Modifier
                .align(Alignment.BottomEnd)
                .size(dot)
                .background(style.accent, CircleShape)
                .border(2.dp, ring, CircleShape)
        )
    }
}
