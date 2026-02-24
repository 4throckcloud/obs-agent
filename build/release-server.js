#!/usr/bin/env node
'use strict';

// ─── OBS Agent Release Server ─────────────────────────────────────────────────
// Tiny HTTP server that runs on the HOST (not in Docker) to execute release.sh.
// Listens on 127.0.0.1:8770 only — accessed via appdev admin proxy.
//
// Usage: node release-server.js
// Or:    systemctl start obs-release-server

const http = require('http');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');

const PORT = 8770;
const HOST = '0.0.0.0';  // Docker containers access via gateway; auth via API key
const RELEASE_SCRIPT = path.join(__dirname, 'release.sh');
const API_KEY_FILE = '/home/ubuntu/production/obs-stack/secrets/internal_api_key.txt';
const MANIFEST_URL = 'https://media.4throck.cloud/agent/manifest.json';
const STAGING_MANIFEST_URL = 'https://media.4throck.cloud/agent/manifest-staging.json';

// Load API key for auth
let API_KEY = '';
try {
    API_KEY = fs.readFileSync(API_KEY_FILE, 'utf8').trim();
} catch (e) {
    console.error(`Failed to read API key from ${API_KEY_FILE}: ${e.message}`);
    process.exit(1);
}

// Track running builds to prevent concurrent runs
let activeBuild = null;

function log(msg) {
    console.log(`[release-server] ${new Date().toISOString()} ${msg}`);
}

function parseBody(req) {
    return new Promise((resolve, reject) => {
        let data = '';
        req.on('data', chunk => {
            data += chunk;
            if (data.length > 4096) { reject(new Error('Body too large')); req.destroy(); }
        });
        req.on('end', () => {
            try { resolve(data ? JSON.parse(data) : {}); }
            catch (e) { reject(new Error('Invalid JSON')); }
        });
        req.on('error', reject);
    });
}

function checkAuth(req) {
    const key = req.headers['x-internal-key'];
    if (!key || key !== API_KEY) return false;
    return true;
}

function sendJson(res, status, data) {
    res.writeHead(status, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(data));
}

// Run release.sh and capture output
function runRelease(version, promote) {
    return new Promise((resolve, reject) => {
        const args = [RELEASE_SCRIPT, version];
        if (promote) args.push('--promote');

        log(`Starting: bash ${args.join(' ')}`);

        const proc = spawn('bash', args, {
            cwd: path.dirname(RELEASE_SCRIPT),
            env: { ...process.env, PATH: process.env.PATH },
            timeout: 600000 // 10 min max
        });

        let output = '';
        let errorOutput = '';

        proc.stdout.on('data', chunk => {
            const text = chunk.toString();
            output += text;
            process.stdout.write(text); // Also log to server console
        });

        proc.stderr.on('data', chunk => {
            const text = chunk.toString();
            errorOutput += text;
            output += text; // Include stderr in combined output
            process.stderr.write(text);
        });

        proc.on('close', code => {
            log(`Process exited with code ${code}`);
            resolve({ code, output, errorOutput });
        });

        proc.on('error', err => {
            reject(err);
        });
    });
}

// Fetch manifest from R2
async function fetchManifest(url) {
    try {
        const resp = await fetch(url, { signal: AbortSignal.timeout(8000) });
        if (!resp.ok) return null;
        return await resp.json();
    } catch (e) {
        return null;
    }
}

const server = http.createServer(async (req, res) => {
    // CORS/security headers
    res.setHeader('X-Content-Type-Options', 'nosniff');
    res.setHeader('Cache-Control', 'no-store');

    const url = new URL(req.url, `http://${HOST}`);
    const pathname = url.pathname;

    // Health check — no auth
    if (req.method === 'GET' && pathname === '/health') {
        return sendJson(res, 200, { status: 'ok', busy: !!activeBuild });
    }

    // All other routes require auth
    if (!checkAuth(req)) {
        return sendJson(res, 401, { error: 'Unauthorized' });
    }

    // GET /status — current manifest info
    if (req.method === 'GET' && pathname === '/status') {
        const [stable, staging] = await Promise.all([
            fetchManifest(MANIFEST_URL),
            fetchManifest(STAGING_MANIFEST_URL)
        ]);
        return sendJson(res, 200, {
            stable: stable || null,
            staging: staging || null,
            busy: !!activeBuild
        });
    }

    // POST /build — build + stage
    if (req.method === 'POST' && pathname === '/build') {
        if (activeBuild) {
            return sendJson(res, 409, { error: 'A build is already running', version: activeBuild });
        }
        let body;
        try { body = await parseBody(req); } catch (e) { return sendJson(res, 400, { error: e.message }); }

        const version = body.version;
        if (!version || !/^[0-9]+\.[0-9]+\.[0-9]+$/.test(version)) {
            return sendJson(res, 400, { error: 'Invalid version format (must be X.Y.Z)' });
        }

        activeBuild = version;
        try {
            const result = await runRelease(version, false);
            activeBuild = null;
            if (result.code === 0) {
                return sendJson(res, 200, { success: true, version, output: result.output });
            } else {
                return sendJson(res, 500, { success: false, version, output: result.output, code: result.code });
            }
        } catch (e) {
            activeBuild = null;
            return sendJson(res, 500, { error: e.message });
        }
    }

    // POST /promote — promote staging to stable
    if (req.method === 'POST' && pathname === '/promote') {
        if (activeBuild) {
            return sendJson(res, 409, { error: 'A build is already running', version: activeBuild });
        }
        let body;
        try { body = await parseBody(req); } catch (e) { return sendJson(res, 400, { error: e.message }); }

        const version = body.version;
        if (!version || !/^[0-9]+\.[0-9]+\.[0-9]+$/.test(version)) {
            return sendJson(res, 400, { error: 'Invalid version format (must be X.Y.Z)' });
        }

        activeBuild = version;
        try {
            const result = await runRelease(version, true);
            activeBuild = null;
            if (result.code === 0) {
                return sendJson(res, 200, { success: true, version, output: result.output });
            } else {
                return sendJson(res, 500, { success: false, version, output: result.output, code: result.code });
            }
        } catch (e) {
            activeBuild = null;
            return sendJson(res, 500, { error: e.message });
        }
    }

    sendJson(res, 404, { error: 'Not found' });
});

server.listen(PORT, HOST, () => {
    log(`Listening on ${HOST}:${PORT}`);
});

process.on('SIGTERM', () => { log('Shutting down'); server.close(); process.exit(0); });
process.on('SIGINT', () => { log('Shutting down'); server.close(); process.exit(0); });
