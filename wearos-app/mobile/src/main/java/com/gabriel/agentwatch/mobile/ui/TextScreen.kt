package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.util.readerBlocks

/**
 * One answer in full, selectable: an agent's markdown for a `transcript`, verbatim text for a
 * `screen` capture ([readerBlocks] — the same rule the watch's reader follows, contracts.md §2).
 *
 * [query] is the prompt that produced it, when the relay still has it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TextScreen(title: String?, text: String, source: String, query: String?, onBack: () -> Unit) {
    val blocks = remember(text, source) { readerBlocks(text, source) }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(title.orEmpty(), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back))
                    }
                }
            )
        }
    ) { padding ->
        SelectionContainer {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(padding)
                    .verticalScroll(rememberScrollState())
                    .padding(16.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp)
            ) {
                if (source == "screen") {
                    Text(
                        stringResource(R.string.reader_screen_source),
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        style = MaterialTheme.typography.labelMedium
                    )
                }
                query?.takeIf { it.isNotBlank() }?.let { AskedBox(it) }
                MarkdownBlocks(blocks)
            }
        }
    }
}
