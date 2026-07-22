package com.gabriel.agentwatch.tile

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.speech.RecognizerIntent
import android.util.Log
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import com.gabriel.agentwatch.network.SseClient

class QuickDictateActivity : ComponentActivity() {

    private val voiceLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            val spokenText = result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
            if (!spokenText.isNullOrEmpty()) {
                sendPromptToAgent(spokenText)
            } else {
                finish()
            }
        } else {
            finish()
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        
        val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
            putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
            putExtra(RecognizerIntent.EXTRA_PROMPT, "Dictate to Agent")
        }
        
        try {
            voiceLauncher.launch(intent)
        } catch (e: Exception) {
            Toast.makeText(this, "Voice dictation not available", Toast.LENGTH_SHORT).show()
            finish()
        }
    }

    private fun sendPromptToAgent(prompt: String) {
        val sharedPreferences = getSharedPreferences("AgentWatchPrefs", Context.MODE_PRIVATE)
        val localIp = sharedPreferences.getString("local_ip", "192.168.1.20") ?: "192.168.1.20"
        val tailscaleIp = sharedPreferences.getString("tailscale_ip", "100.64.0.1") ?: "100.64.0.1"

        // Create a temporary client just to send the input. 
        // We don't need to listen to SSE for sending.
        val tempClient = SseClient(localIp, tailscaleIp)
        tempClient.sendInputCommand(prompt) { success ->
            runOnUiThread {
                if (success) {
                    Toast.makeText(this@QuickDictateActivity, "Prompt sent!", Toast.LENGTH_SHORT).show()
                } else {
                    Toast.makeText(this@QuickDictateActivity, "Failed to send", Toast.LENGTH_SHORT).show()
                }
                finish()
            }
        }
    }
}
