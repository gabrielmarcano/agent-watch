package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.wear.compose.material.*
import com.gabriel.agentwatch.approval.permissionButtons
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.ui.theme.BrightGreen
import com.gabriel.agentwatch.ui.theme.BrightYellow
import com.gabriel.agentwatch.ui.theme.Red400

@Composable
fun PromptCard(
    agent: AgentState,
    onAnswerClick: (optionId: String) -> Unit,
    onCancelClick: () -> Unit,
    isActionInFlight: Boolean,
    modifier: Modifier = Modifier,
    isSent: Boolean = false
) {
    val prompt = agent.prompt ?: return
    var showAllOptions by remember { mutableStateOf(false) }

    Card(
        onClick = {},
        modifier = modifier
            .fillMaxWidth()
            .border(BorderStroke(1.dp, BrightYellow.copy(alpha = 0.5f)), shape = RoundedCornerShape(16.dp))
            .padding(vertical = 4.dp),
        backgroundPainter = CardDefaults.cardBackgroundPainter(
            startBackgroundColor = BrightYellow.copy(alpha = 0.12f),
            endBackgroundColor = BrightYellow.copy(alpha = 0.03f)
        )
    ) {
        Column(modifier = Modifier.fillMaxWidth()) {
            // Header badge
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.fillMaxWidth()
            ) {
                Text(
                    text = "⚠ ${prompt.kind.uppercase()}",
                    style = MaterialTheme.typography.caption2.copy(
                        fontSize = 8.5.sp,
                        fontWeight = FontWeight.ExtraBold,
                        letterSpacing = 1.sp
                    ),
                    color = BrightYellow
                )
            }

            Spacer(modifier = Modifier.height(4.dp))

            // Title
            if (prompt.title.isNotBlank()) {
                Text(
                    text = prompt.title,
                    style = MaterialTheme.typography.body2.copy(
                        fontSize = 11.5.sp,
                        fontWeight = FontWeight.Bold
                    ),
                    color = Color.White
                )
                Spacer(modifier = Modifier.height(2.dp))
            }

            // Detail or raw_tail
            val detailText = prompt.detail ?: prompt.raw_tail
            if (!detailText.isNullOrBlank()) {
                Text(
                    text = detailText,
                    style = MaterialTheme.typography.caption2.copy(
                        fontSize = 9.5.sp,
                        fontFamily = FontFamily.Monospace,
                        lineHeight = 12.sp
                    ),
                    color = Color.White.copy(alpha = 0.85f),
                    maxLines = 6
                )
                Spacer(modifier = Modifier.height(6.dp))
            }

            // Answer accepted; buttons stay disabled until the agent's state changes
            if (isSent) {
                Text(
                    text = "Sent…",
                    style = MaterialTheme.typography.caption2.copy(fontSize = 9.5.sp, fontWeight = FontWeight.Bold),
                    color = BrightGreen,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.fillMaxWidth()
                )
                Spacer(modifier = Modifier.height(4.dp))
            }

            // Options rendering based on kind
            when (prompt.kind) {
                "permission" -> {
                    if (showAllOptions) {
                        // Show full list of options
                        prompt.options.forEach { opt ->
                            val label = if (opt.role == "allow_always") "${opt.label} (always)" else opt.label
                            Chip(
                                onClick = { onAnswerClick(opt.id) },
                                enabled = !isActionInFlight,
                                label = { Text(label, fontSize = 10.sp, maxLines = 2) },
                                colors = ChipDefaults.chipColors(
                                    backgroundColor = when (opt.role) {
                                        "allow_once", "allow_always" -> BrightGreen.copy(alpha = 0.8f)
                                        "deny" -> Red400.copy(alpha = 0.8f)
                                        else -> Color(0x33FFFFFF)
                                    }
                                ),
                                modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                            )
                        }
                    } else {
                        // Allow / Deny buttons, chosen by role (ALLOW is allow_once only)
                        val buttons = permissionButtons(prompt)
                        val allowOpt = buttons.allow
                        val denyOpt = buttons.deny

                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.spacedBy(4.dp)
                        ) {
                            if (allowOpt != null) {
                                Button(
                                    onClick = { onAnswerClick(allowOpt.id) },
                                    enabled = !isActionInFlight,
                                    colors = ButtonDefaults.buttonColors(backgroundColor = BrightGreen),
                                    modifier = Modifier.weight(1f)
                                ) {
                                    Text("ALLOW", fontSize = 10.sp, fontWeight = FontWeight.Bold, color = Color.Black)
                                }
                            }

                            Button(
                                onClick = {
                                    if (denyOpt != null) onAnswerClick(denyOpt.id)
                                    else onCancelClick()
                                },
                                enabled = !isActionInFlight,
                                colors = ButtonDefaults.buttonColors(backgroundColor = Red400),
                                modifier = Modifier.weight(1f)
                            ) {
                                Text("DENY", fontSize = 10.sp, fontWeight = FontWeight.Bold, color = Color.White)
                            }
                        }

                        // "More..." whenever an option is not on ALLOW/DENY (e.g. allow_always)
                        if (buttons.hasMore) {
                            Spacer(modifier = Modifier.height(2.dp))
                            Chip(
                                onClick = { showAllOptions = true },
                                enabled = !isActionInFlight,
                                label = {
                                    Text(
                                        "MORE OPTIONS...",
                                        fontSize = 9.sp,
                                        textAlign = TextAlign.Center,
                                        modifier = Modifier.fillMaxWidth()
                                    )
                                },
                                colors = ChipDefaults.chipColors(backgroundColor = Color(0x1AFFFFFF)),
                                modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                            )
                        }
                    }
                }
                "question" -> {
                    // Question: One chip per option + Cancel at the bottom
                    prompt.options.forEach { opt ->
                        Chip(
                            onClick = { onAnswerClick(opt.id) },
                            enabled = !isActionInFlight,
                            label = { Text(opt.label, fontSize = 10.sp, maxLines = 2) },
                            colors = ChipDefaults.chipColors(backgroundColor = Color(0x26FFFFFF)),
                            modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                        )
                    }

                    Spacer(modifier = Modifier.height(2.dp))
                    Chip(
                        onClick = onCancelClick,
                        enabled = !isActionInFlight,
                        label = {
                            Text(
                                "CANCEL",
                                fontSize = 9.5.sp,
                                textAlign = TextAlign.Center,
                                modifier = Modifier.fillMaxWidth()
                            )
                        },
                        colors = ChipDefaults.chipColors(backgroundColor = Red400.copy(alpha = 0.4f)),
                        modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                    )
                }
                else -> {
                    // Unknown prompt
                    Text(
                        text = "Answer this on the computer",
                        style = MaterialTheme.typography.caption2.copy(fontSize = 9.sp),
                        color = Color.White.copy(alpha = 0.6f)
                    )
                    Spacer(modifier = Modifier.height(4.dp))
                    Chip(
                        onClick = onCancelClick,
                        enabled = !isActionInFlight,
                        label = {
                            Text(
                                "CANCEL",
                                fontSize = 9.5.sp,
                                textAlign = TextAlign.Center,
                                modifier = Modifier.fillMaxWidth()
                            )
                        },
                        colors = ChipDefaults.chipColors(backgroundColor = Red400.copy(alpha = 0.4f)),
                        modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)
                    )
                }
            }
        }
    }
}
