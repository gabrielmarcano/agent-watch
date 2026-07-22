const fs = require('fs');
const path = require('path');
const http = require('http');

const brainPath = path.join(process.env.HOME, '.gemini', 'antigravity-cli', 'brain');
const webhookUrl = 'http://localhost:8420/webhook';

function sendWebhook(type, message = '') {
  const payload = JSON.stringify({ type, message });
  const req = http.request(webhookUrl, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Content-Length': payload.length }
  });
  req.on('error', (e) => console.error(`Webhook error: ${e.message}`));
  req.write(payload);
  req.end();
}

// Find the latest transcript
function getLatestTranscript() {
  if (!fs.existsSync(brainPath)) return null;
  const dirs = fs.readdirSync(brainPath).filter(d => fs.statSync(path.join(brainPath, d)).isDirectory());
  
  let latestFile = null;
  let latestTime = 0;
  
  for (const dir of dirs) {
    const file = path.join(brainPath, dir, '.system_generated', 'logs', 'transcript.jsonl');
    if (fs.existsSync(file)) {
      const stats = fs.statSync(file);
      if (stats.mtimeMs > latestTime) {
        latestTime = stats.mtimeMs;
        latestFile = file;
      }
    }
  }
  return latestFile;
}

const targetFile = getLatestTranscript();
if (!targetFile) {
  console.error('No AGY transcript found. Make sure AGY has been run at least once.');
  process.exit(1);
}

console.log(`[AGY SIDECAR] Monitoring AGY transcript: ${targetFile}`);
console.log(`[AGY SIDECAR] Forwarding events to ${webhookUrl}`);

// We only want to tail new changes, so start reading from the current file size end
let lastSize = fs.statSync(targetFile).size;

setInterval(() => {
  try {
    const stats = fs.statSync(targetFile);
    
    // If file was truncated (rare for transcripts, but possible)
    if (stats.size < lastSize) {
      lastSize = stats.size;
    }
    
    if (stats.size > lastSize) {
      // Read the newly added bytes
      const stream = fs.createReadStream(targetFile, { start: lastSize, end: stats.size - 1 });
      let data = '';
      
      stream.on('data', chunk => { data += chunk; });
      stream.on('end', () => {
        lastSize = stats.size;
        
        // Process new lines
        const lines = data.split('\n').filter(l => l.trim() !== '');
        for (const line of lines) {
          try {
            const step = JSON.parse(line);
            
            if (step.type === 'USER_INPUT') {
              console.log('[AGY] User Input Detected -> Sending SessionStart');
              sendWebhook('SessionStart', 'AGY started thinking...');
            } 
            else if (step.type === 'PLANNER_RESPONSE') {
              // If AGY is using tools, it's still "Thinking/Working"
              if (step.tool_calls && step.tool_calls.length > 0) {
                 console.log(`[AGY] Tool Call Detected (${step.tool_calls[0].name}) -> Sending PostToolUse`);
                 sendWebhook('PostToolUse', 'AGY is running a command...');
              } 
              // If there are no tool calls in a PLANNER_RESPONSE, AGY is done talking to the user
              else {
                 console.log('[AGY] Final Response Detected -> Sending Stop');
                 sendWebhook('Stop', 'AGY has finished responding.');
              }
            }
          } catch (e) {
            // Ignore JSON parse errors for incomplete lines written mid-flush
          }
        }
      });
    }
  } catch (err) {
    console.error(`Error reading transcript: ${err.message}`);
  }
}, 500);
