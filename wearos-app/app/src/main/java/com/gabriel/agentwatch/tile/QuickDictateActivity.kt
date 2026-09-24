package com.gabriel.agentwatch.tile

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.speech.RecognizerIntent
import android.util.Log
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.lifecycle.lifecycleScope
import androidx.wear.compose.material.CircularProgressIndicator
import androidx.wear.compose.material.MaterialTheme
import androidx.wear.compose.material.Text
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.resolveTargetAgent
import com.gabriel.agentwatch.network.RelayClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class QuickDictateActivity : ComponentActivity() {

    companion object {
        private const val TAG = "QuickDictate"
    }

    private var targetAgent: AgentState? = null
    private var statusText by mutableStateOf("Connecting...")

    private val voiceLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            val spokenText = result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
            if (!spokenText.isNullOrEmpty()) {
                val agent = targetAgent
                if (agent != null) {
                    sendPromptToAgent(agent, spokenText)
                } else {
                    finish()
                }
            } else {
                finish()
            }
        } else {
            finish()
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val prefs = Prefs(this)
        if (!prefs.isPaired) {
            Toast.makeText(this, "Not paired with relay", Toast.LENGTH_SHORT).show()
            finish()
            return
        }

        setContent {
            MaterialTheme {
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .background(Color.Black),
                    contentAlignment = Alignment.Center
                ) {
                    Column(
                        horizontalAlignment = Alignment.CenterHorizontally,
                        modifier = Modifier.padding(16.dp)
                    ) {
                        CircularProgressIndicator()
                        Spacer(modifier = Modifier.height(12.dp))
                        Text(
                            text = statusText,
                            style = MaterialTheme.typography.body2,
                            color = Color.White
                        )
                    }
                }
            }
        }

        resolveAndLaunchDictation(prefs)
    }

    private fun resolveAndLaunchDictation(prefs: Prefs) {
        lifecycleScope.launch {
            try {
                val client = RelayClient(prefs.relayUrl, prefs.deviceToken)
                val result = withContext(Dispatchers.IO) { client.agents() }

                result.fold(
                    onSuccess = { snapshot ->
                        val target = resolveTargetAgent(snapshot.agents, prefs.pinnedPaneId)
                        if (target == null) {
                            Toast.makeText(this@QuickDictateActivity, "No active agents", Toast.LENGTH_SHORT).show()
                            finish()
                            return@fold
                        }
                        targetAgent = target

                        val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
                            putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
                            putExtra(RecognizerIntent.EXTRA_PROMPT, "To: ${target.label}")
                        }
                        try {
                            voiceLauncher.launch(intent)
                        } catch (e: Exception) {
                            Log.e(TAG, "Voice dictation not available", e)
                            Toast.makeText(this@QuickDictateActivity, "Voice dictation not available", Toast.LENGTH_SHORT).show()
                            finish()
                        }
                    },
                    onFailure = { error ->
                        Log.e(TAG, "Failed to resolve target agent", error)
                        Toast.makeText(this@QuickDictateActivity, "Relay unreachable: ${error.message}", Toast.LENGTH_SHORT).show()
                        finish()
                    }
                )
            } catch (e: Exception) {
                Log.e(TAG, "Error resolving target agent", e)
                Toast.makeText(this@QuickDictateActivity, "Error: ${e.message}", Toast.LENGTH_SHORT).show()
                finish()
            }
        }
    }

    private fun sendPromptToAgent(agent: AgentState, text: String) {
        statusText = "Sending to ${agent.label}..."
        val prefs = Prefs(this)
        lifecycleScope.launch {
            try {
                val client = RelayClient(prefs.relayUrl, prefs.deviceToken)
                val freshSeq = withContext(Dispatchers.IO) {
                    val freshAgents = client.agents().getOrNull()?.agents
                    val freshAgent = freshAgents?.find { it.pane_id == agent.pane_id }
                    freshAgent?.state_change_seq ?: agent.state_change_seq
                }

                val sendResult = withContext(Dispatchers.IO) {
                    client.prompt(agent.pane_id, text, freshSeq)
                }

                sendResult.fold(
                    onSuccess = {
                        Toast.makeText(this@QuickDictateActivity, "Sent to ${agent.label}", Toast.LENGTH_SHORT).show()
                    },
                    onFailure = { error ->
                        Toast.makeText(this@QuickDictateActivity, "Failed: ${error.message}", Toast.LENGTH_LONG).show()
                    }
                )
            } catch (e: Exception) {
                Toast.makeText(this@QuickDictateActivity, "Error: ${e.message}", Toast.LENGTH_SHORT).show()
            } finally {
                finish()
            }
        }
    }
}
