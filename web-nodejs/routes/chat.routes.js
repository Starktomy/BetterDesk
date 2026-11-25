/**
 * BetterDesk Console — Chat Routes
 * Dedicated chat page + API endpoints for Chat 2.0
 */

'use strict';

const express = require('express');
const router = express.Router();
const multer = require('multer');
const path = require('path');
const fs = require('fs');
const crypto = require('crypto');
const { requireAuth, requirePermission } = require('../middleware/auth');
const { uploadLimiter, fileAccessLimiter } = require('../middleware/rateLimiter');

// File upload config — max 50MB, store in data/chat-files/
const UPLOAD_DIR = path.join(__dirname, '..', 'data', 'chat-files');
const MAX_FILE_SIZE = 50 * 1024 * 1024; // 50MB
// The 128-bit random ID is the only thing that lets a participant fetch a
// file, so a request must name the whole ID and never a prefix of it.
const STORED_NAME_RE = /^([a-f0-9]{32})(\.[a-z0-9]{1,10})?$/;

if (!fs.existsSync(UPLOAD_DIR)) {
    fs.mkdirSync(UPLOAD_DIR, { recursive: true });
}

function safeExtension(originalName) {
    const ext = path.extname(String(originalName || '')).toLowerCase();
    return /^\.[a-z0-9]{1,10}$/.test(ext) ? ext : '';
}

async function resolveChatFile(requested) {
    const match = STORED_NAME_RE.exec(String(requested || ''));
    if (!match) return null;
    let name = match[0];
    if (!match[2]) {
        // Links created before the extension was part of the URL carry only
        // the ID. IDs are unique, so at most one stored file can match.
        const id = match[1];
        const entries = await fs.promises.readdir(UPLOAD_DIR);
        name = entries.find((entry) => entry === id || entry.startsWith(`${id}.`));
        if (!name) return null;
    }
    const filePath = path.join(UPLOAD_DIR, name);
    try {
        const stat = await fs.promises.lstat(filePath);
        return stat.isFile() ? filePath : null;
    } catch (_) {
        return null;
    }
}

const upload = multer({
    storage: multer.diskStorage({
        destination: (_req, _file, cb) => cb(null, UPLOAD_DIR),
        filename: (_req, file, cb) => {
            const id = crypto.randomBytes(16).toString('hex');
            cb(null, `${id}${safeExtension(file.originalname)}`);
        },
    }),
    limits: { fileSize: MAX_FILE_SIZE },
});

// ========== Page Route ==========

router.get('/chat', requireAuth, (req, res) => {
    res.render('chat', {
        title: req.t('nav.chat'),
        pageStyles: ['chat'],
        pageScripts: ['chat'],
        currentPage: 'chat',
        breadcrumb: [{ label: req.t('nav.chat') }],
    });
});

// ========== API Routes ==========

// Upload a chat attachment. Files are stored as sent: the browser does not
// encrypt them, so anyone with server access can read them.
router.post('/api/chat/upload', uploadLimiter, requireAuth, requirePermission('chat.access'), upload.single('file'), (req, res) => {
    if (!req.file) {
        return res.status(400).json({ success: false, error: 'No file provided' });
    }
    const fileId = path.basename(req.file.filename, path.extname(req.file.filename));
    res.json({
        success: true,
        file_id: fileId,
        filename: req.file.filename,
        size: req.file.size,
        url: `/api/chat/files/${req.file.filename}`,
    });
});

// Download file
router.get('/api/chat/files/:fileId', fileAccessLimiter, requireAuth, requirePermission('chat.access'), async (req, res) => {
    try {
        const filePath = await resolveChatFile(req.params.fileId);
        if (!filePath) {
            return res.status(404).json({ success: false, error: 'File not found' });
        }
        res.download(filePath);
    } catch (err) {
        console.error('Chat file download error:', err.message);
        res.status(500).json({ success: false, error: 'File not available' });
    }
});

// Search messages via Go server proxy
router.get('/api/chat/search', requireAuth, requirePermission('chat.access'), async (req, res) => {
    try {
        const { apiClient } = require('../services/betterdeskApi');
        const query = String(req.query.q || '').slice(0, 200);
        const conversationId = req.query.conversation_id || '';
        if (!query) return res.json({ success: true, messages: [] });

        const resp = await apiClient.get('/api/chat/search', {
            params: { q: query, conversation_id: conversationId, limit: 50 },
        });
        res.json({ success: true, messages: resp.data?.messages || [] });
    } catch (e) {
        res.json({ success: true, messages: [] });
    }
});

module.exports = router;
