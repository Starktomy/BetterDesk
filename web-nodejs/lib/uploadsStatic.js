'use strict';

/**
 * Static handler for /uploads (branding logos and backgrounds).
 *
 * The directory shares the panel's origin, so a stored HTML or script file
 * would run with the operator's session, and `script-src 'self'` would let
 * any injected markup load it. Only image extensions are served, and every
 * response carries a sandboxing CSP: an SVG opened directly cannot run
 * script, while <img> and CSS backgrounds keep working.
 */

const express = require('express');

const SERVED_UPLOAD_RE = /^\/[A-Za-z0-9_-]+\.(?:png|jpe?g|jfif|gif|webp|svg|ico|bmp|avif)$/i;
const UPLOADS_CSP = "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox";

function createUploadsStatic(dir, { maxAge = 0 } = {}) {
    const serve = express.static(dir, {
        maxAge,
        etag: true,
        index: false,
        dotfiles: 'deny',
        setHeaders: (res) => {
            res.setHeader('Content-Security-Policy', UPLOADS_CSP);
            res.setHeader('X-Content-Type-Options', 'nosniff');
        }
    });
    return function uploadsStatic(req, res, next) {
        if (!SERVED_UPLOAD_RE.test(req.path)) {
            return res.status(404).end();
        }
        return serve(req, res, next);
    };
}

module.exports = { createUploadsStatic, SERVED_UPLOAD_RE, UPLOADS_CSP };
