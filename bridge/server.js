const http = require('node:http');
const https = require('node:https');
const fs = require('node:fs');
const readline = require('node:readline');
const { exec, fork } = require('node:child_process');
const crypto = require('node:crypto');
const path = require('node:path');

// Load Pushover Configuration
let config = {};
try {
  const configPath = path.join(__dirname, 'config.json');
  config = JSON.parse(fs.readFileSync(configPath, 'utf8'));
} catch (e) {
  config = {
    pushover_user: process.env.PUSHOVER_USER,
    pushover_token: process.env.PUSHOVER_TOKEN
  };
}

const PORT = 8420;

// Global state tracking the active Claude Code session
let globalState = {
  status: 'idle', // 'idle' | 'thinking' | 'waiting_for_permission' | 'done'
  session_id: null,
  cwd: null,
  last_query: null,
  last_response: null,
  history: [],
  tool_name: null,
  tool_input: null,
  timestamp: new Date().toISOString()
};

// SSE active clients
let clients = [];

// Send a keep-alive ping every 15 seconds to prevent connection timeouts
setInterval(() => {
  clients.forEach(c => {
    try {
      c.write(':\n\n'); // SSE comment keep-alive
    } catch (e) {
      // Failures will be cleaned up by req.on('close')
    }
  });
}, 15000);

function updateState(newState) {
  globalState = {
    ...globalState,
    ...newState,
    timestamp: new Date().toISOString()
  };
  console.log(`[STATE CHANGE] Status: ${globalState.status}`);
  sendEventToAll('state', globalState);
}

function sendEventToAll(event, data) {
  const payload = `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
  clients.forEach(c => c.write(payload));
}

// Parse transcript JSONL to extract query and response
async function parseTranscript(filePath) {
  if (!filePath || !fs.existsSync(filePath)) {
    return { query: null, response: null };
  }

  try {
    const fileStream = fs.createReadStream(filePath);
    const rl = readline.createInterface({
      input: fileStream,
      crlfDelay: Infinity
    });

    const messages = [];
    for await (const line of rl) {
      if (!line.trim()) continue;
      try {
        messages.push(JSON.parse(line));
      } catch (e) {
        // Skip malformed lines
      }
    }

    let lastUser = null;
    let lastAssistant = null;

    // Search backwards for the last user prompt and assistant response
    for (let i = messages.length - 1; i >= 0; i--) {
      const msg = messages[i];
      if (msg.type === 'user' && !lastUser) {
        const content = msg.message?.content;
        if (typeof content === 'string') {
          lastUser = content;
        } else if (Array.isArray(content)) {
          const textBlocks = content.filter(c => c.type === 'text');
          if (textBlocks.length > 0) {
            lastUser = textBlocks.map(tb => tb.text).join(' ');
          }
        }
      }
      if (msg.type === 'assistant' && msg.message?.content && !lastAssistant) {
        const content = msg.message?.content;
        if (Array.isArray(content)) {
          const textBlocks = content.filter(c => c.type === 'text');
          if (textBlocks.length > 0) {
            lastAssistant = textBlocks.map(tb => tb.text).join(' ');
          }
        }
      }
      if (lastUser && lastAssistant) break;
    }

    return {
      query: lastUser,
      response: lastAssistant
    };
  } catch (err) {
    console.error('Error parsing transcript:', err);
    return { query: null, response: null };
  }
}

// Send keyboard input to the target terminal using AppleScript
function sendInputToTerminal(text) {
  const targetTerminal = config.target_terminal || 'Warp';
  console.log(`[INPUT] Sending to ${targetTerminal}: "${text}"`);
  
  // Escape backslashes and double quotes for AppleScript string
  const escapedText = text.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
  
  const appleScript = [
    `tell application "${targetTerminal}" to activate`,
    'delay 0.1',
    'tell application "System Events"',
    `  keystroke "${escapedText}"`,
    '  key code 36',
    'end tell'
  ].map(line => `-e "${line.replace(/"/g, '\\"')}"`).join(' ');

  exec(`osascript ${appleScript}`, (err, stdout, stderr) => {
    if (err) {
      console.error('AppleScript error:', stderr);
    } else {
      console.log(`Successfully sent input to ${targetTerminal}`);
    }
  });
}

let registeredFcmToken = null;
let cachedGoogleToken = null;
let googleTokenExpiry = 0;

// Fetch Google OAuth2 Access Token for FCM V1 API natively
function getGoogleAccessToken() {
  return new Promise((resolve, reject) => {
    const now = Math.floor(Date.now() / 1000);
    // Cache token for 55 minutes
    if (cachedGoogleToken && now < googleTokenExpiry - 60) {
      return resolve(cachedGoogleToken);
    }

    try {
      const serviceAccountPath = path.join(__dirname, 'firebase-service-account.json');
      if (!fs.existsSync(serviceAccountPath)) {
        return reject(new Error('firebase-service-account.json not found'));
      }
      const serviceAccount = JSON.parse(fs.readFileSync(serviceAccountPath, 'utf8'));

      const header = { alg: 'RS256', typ: 'JWT' };
      const claim = {
        iss: serviceAccount.client_email,
        scope: 'https://www.googleapis.com/auth/firebase.messaging',
        aud: 'https://oauth2.googleapis.com/token',
        exp: now + 3600,
        iat: now
      };

      const base64UrlEncode = (obj) => {
        return Buffer.from(JSON.stringify(obj))
          .toString('base64')
          .replace(/=/g, '')
          .replace(/\+/g, '-')
          .replace(/\//g, '_');
      };

      const signatureInput = `${base64UrlEncode(header)}.${base64UrlEncode(claim)}`;

      const sign = crypto.createSign('RSA-SHA256');
      sign.update(signatureInput);
      const signature = sign.sign(serviceAccount.private_key, 'base64')
        .replace(/=/g, '')
        .replace(/\+/g, '-')
        .replace(/\//g, '_');

      const assertion = `${signatureInput}.${signature}`;
      const postData = `grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion=${assertion}`;

      const options = {
        hostname: 'oauth2.googleapis.com',
        port: 443,
        path: '/token',
        method: 'POST',
        headers: {
          'Content-Type': 'application/x-www-form-urlencoded',
          'Content-Length': Buffer.byteLength(postData)
        }
      };

      const req = https.request(options, (res) => {
        let body = '';
        res.on('data', chunk => body += chunk);
        res.on('end', () => {
          try {
            const data = JSON.parse(body);
            if (data.access_token) {
              cachedGoogleToken = data.access_token;
              googleTokenExpiry = now + (data.expires_in || 3600);
              resolve(cachedGoogleToken);
            } else {
              reject(new Error(`OAuth Token exchange failed: ${body}`));
            }
          } catch (e) {
            reject(e);
          }
        });
      });

      req.on('error', reject);
      req.write(postData);
      req.end();
    } catch (err) {
      reject(err);
    }
  });
}

// Send push notification via FCM V1 API
function sendFcmV1Notification(token, title, message, eventType) {
  getGoogleAccessToken()
    .then((accessToken) => {
      try {
        const serviceAccountPath = path.join(__dirname, 'firebase-service-account.json');
        const serviceAccount = JSON.parse(fs.readFileSync(serviceAccountPath, 'utf8'));
        const projectId = serviceAccount.project_id;

        const postData = JSON.stringify({
          message: {
            token: token,
            data: {
              title: title,
              body: message,
              event: eventType
            }
          }
        });

        const options = {
          hostname: 'fcm.googleapis.com',
          port: 443,
          path: `/v1/projects/${projectId}/messages:send`,
          method: 'POST',
          headers: {
            'Authorization': `Bearer ${accessToken}`,
            'Content-Type': 'application/json',
            'Content-Length': Buffer.byteLength(postData)
          }
        };

        const req = https.request(options, (res) => {
          let body = '';
          res.on('data', chunk => body += chunk);
          res.on('end', () => {
            console.log(`[FCM V1] Send response: ${res.statusCode} - ${body}`);
          });
        });

        req.on('error', (e) => {
          console.error(`[FCM V1] Error sending message: ${e.message}`);
        });

        req.write(postData);
        req.end();
      } catch (err) {
        console.error('[FCM V1] Error preparing message payload:', err.message);
      }
    })
    .catch((err) => {
      console.error('[FCM V1] Failed to retrieve Google OAuth access token:', err.message);
    });
}

// Send push notification via Pushover API
function sendPushoverNotification(title, message, priority = 0) {
  const user = config.pushover_user || process.env.PUSHOVER_USER;
  const token = config.pushover_token || process.env.PUSHOVER_TOKEN;

  if (!user || !token || user.startsWith('INTRODUCE_') || token.startsWith('INTRODUCE_')) {
    console.log('[PUSHOVER] Not sent: User Key or API Token is not configured in config.json.');
    return;
  }

  const postData = JSON.stringify({
    token: token,
    user: user,
    title: title,
    message: message,
    priority: priority
  });

  const options = {
    hostname: 'api.pushover.net',
    port: 443,
    path: '/1/messages.json',
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Content-Length': Buffer.byteLength(postData)
    }
  };

  const req = https.request(options, (res) => {
    let body = '';
    res.on('data', chunk => body += chunk);
    res.on('end', () => {
      console.log(`[PUSHOVER] Response status: ${res.statusCode} - ${body}`);
    });
  });

  req.on('error', (e) => {
    console.error(`[PUSHOVER] Error sending notification: ${e.message}`);
  });

  req.write(postData);
  req.end();
}

// General function to route alerts through FCM V1 (if watch registered) or Pushover fallback
function sendPushNotification(title, message, priority = 0, eventType = 'Notification') {
  const serviceAccountPath = path.join(__dirname, 'firebase-service-account.json');
  const hasFcmV1 = registeredFcmToken && fs.existsSync(serviceAccountPath);

  if (hasFcmV1) {
    console.log(`[PUSH] Dispatching FCM V1 directly to Wear OS for event "${eventType}"`);
    sendFcmV1Notification(registeredFcmToken, title, message, eventType);
  } else {
    console.log('[PUSH] Dispatching via Pushover fallback');
    sendPushoverNotification(title, message, priority);
  }
}

const server = http.createServer((req, res) => {
  // CORS Headers
  res.setHeader('Access-Control-Allow-Origin', '*');
  res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
  res.setHeader('Access-Control-Allow-Headers', 'Content-Type');

  if (req.method === 'OPTIONS') {
    res.writeHead(204);
    res.end();
    return;
  }

  // SSE stream endpoint for Wear OS app to listen to events
  if (req.method === 'GET' && req.url === '/events') {
    res.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      'Connection': 'keep-alive'
    });

    // Send initial state immediately
    res.write(`event: state\ndata: ${JSON.stringify(globalState)}\n\n`);
    clients.push(res);
    console.log(`[SSE] Client connected. Active clients: ${clients.length}`);

    req.on('close', () => {
      clients = clients.filter(c => c !== res);
      console.log(`[SSE] Client disconnected. Active clients: ${clients.length}`);
    });
    return;
  }

  // Get current state
  if (req.method === 'GET' && req.url === '/state') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(globalState));
    return;
  }

  // Endpoint to register the watch's FCM token
  if (req.method === 'POST' && req.url === '/register') {
    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end', () => {
      try {
        const payload = JSON.parse(body);
        if (payload.token) {
          registeredFcmToken = payload.token;
          console.log(`[FCM] Successfully registered watch token: ${registeredFcmToken.substring(0, 15)}...`);
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ success: true }));
        } else {
          res.writeHead(400, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ error: 'Missing "token" field' }));
        }
      } catch (err) {
        res.writeHead(400, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: 'Invalid JSON' }));
      }
    });
    return;
  }

  // Endpoint to receive commands/input from the watch
  if (req.method === 'POST' && req.url === '/input') {
    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end', () => {
      try {
        const payload = JSON.parse(body);
        if (payload.text) {
          sendInputToTerminal(payload.text);
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ success: true }));
        } else {
          res.writeHead(400, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ error: 'Missing "text" field' }));
        }
      } catch (err) {
        res.writeHead(400, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: 'Invalid JSON' }));
      }
    });
    return;
  }

  // Endpoint for Claude Code hooks to report events
  if (req.method === 'POST' && req.url === '/webhook') {
    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end', async () => {
      try {
        const payload = JSON.parse(body);
        const event = payload.event;
        const data = payload.data || {};

        console.log(`[WEBHOOK] Received event: ${event}`);

        switch (event) {
          case 'SessionStart':
            updateState({
              status: 'idle',
              session_id: data.session_id,
              cwd: data.cwd,
              last_query: null,
              last_response: null,
              tool_name: null,
              tool_input: null
            });
            break;

          case 'UserPromptSubmit':
            updateState({
              status: 'thinking',
              last_query: data.prompt,
              tool_name: null,
              tool_input: null
            });
            break;

          case 'PostToolUse':
            updateState({
              status: 'thinking'
            });
            break;

          case 'PermissionRequest':
            updateState({
              status: 'waiting_for_permission',
              tool_name: data.tool_name,
              tool_input: data.tool_input
            });
            const cmd = data.tool_input?.command || data.tool_input?.file_path || '';
            const msg = `Wants to run ${data.tool_name}` + (cmd ? `: ${cmd}` : '') + ' (respond y/n from watch)';
            sendPushNotification('Claude: Permission Request', msg, 1, 'PermissionRequest');
            break;

          case 'Notification':
            if (data.notification_type === 'idle_prompt') {
              updateState({
                status: 'idle'
              });
              sendPushNotification('Claude: Waiting for input', data.message || 'Claude is waiting for you.', 0, 'Notification');
            }
            break;

          case 'Stop':
            // Small delay to let transcript flush to disk
            setTimeout(async () => {
              const { query, response } = await parseTranscript(data.transcript_path);
              const q = query || globalState.last_query;
              const r = response;
              
              const currentHistory = globalState.history || [];
              const newItem = {
                id: Date.now().toString(),
                query: q,
                response: r,
                timestamp: new Date().toISOString()
              };
              
              // Keep only the last 3 items
              const newHistory = [...currentHistory, newItem].slice(-3);

              updateState({
                status: 'done',
                last_query: q,
                last_response: r,
                history: newHistory,
                tool_name: null,
                tool_input: null
              });

              // Clean markdown for notification payload
              const cleanMsg = r ? (r.replace(/\*\*(.*?)\*\*/g, '$1').replace(/`{3}[\s\S]*?`{3}/g, '[Code]').trim()) : 'Task completed.';
              sendPushNotification('Claude Code', cleanMsg, 0, 'Stop');
            }, 300);
            break;

          default:
            console.log(`Unhandled event: ${event}`);
        }

        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ success: true }));
      } catch (err) {
        console.error('Webhook error:', err);
        res.writeHead(400, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: 'Invalid JSON or processing error' }));
      }
    });
    return;
  }

  // Not found
  res.writeHead(404);
  res.end();
});

server.listen(PORT, () => {
  console.log(`Bridge server listening on http://localhost:${PORT}`);
  
  // Launch AGY Sidecar automatically
  const sidecarPath = path.join(__dirname, 'agy-sidecar.js');
  if (fs.existsSync(sidecarPath)) {
    console.log('[BRIDGE] Automatically launching AGY Sidecar...');
    const sidecar = fork(sidecarPath);
    sidecar.on('error', (err) => {
      console.error('[AGY SIDECAR] Failed to start:', err);
    });
    sidecar.on('exit', (code) => {
      console.log(`[AGY SIDECAR] Exited with code ${code}`);
    });
  }
});
