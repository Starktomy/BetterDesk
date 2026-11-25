/**
 * BetterDesk Console - Security Middleware
 * Configures Helmet and custom security headers
 */

const crypto = require('crypto');
const helmet = require('helmet');
const config = require('../config/config');

// Pages load only xterm from jsDelivr. Allowing the whole CDN would let any
// injected markup load arbitrary npm or GitHub code past the nonce, so the
// source is limited to the @xterm scope (a trailing "/" matches by prefix).
const XTERM_CDN = 'https://cdn.jsdelivr.net/npm/@xterm/';

/**
 * HSTS is disabled for self-signed / LAN installs so HTTP↔HTTPS toggles do not
 * leave a year-long browser enforcement (#219). Set HSTS_ENABLED=true to force it.
 */
function shouldSendStrictTransportSecurity() {
    if (!config.httpsEnabled) return false;
    const hstsEnv = String(process.env.HSTS_ENABLED || '').toLowerCase();
    if (hstsEnv === 'false' || hstsEnv === '0' || hstsEnv === 'off') return false;
    if (hstsEnv === 'true' || hstsEnv === '1' || hstsEnv === 'on') return true;
    return !config.allowSelfSignedCerts;
}

/**
 * Emergency rollback for RdClient only: CSP_RELAX_REMOTE=true re-enables
 * unsafe-eval on /remote* (default off after static protobuf bundle).
 */
function allowRemoteUnsafeEval() {
    return String(process.env.CSP_RELAX_REMOTE || '').toLowerCase() === 'true'
        || process.env.CSP_RELAX_REMOTE === '1';
}

function createHelmet({ remoteEval, https, hsts }) {
    const scriptSources = ["'self'", (_req, res) => `'nonce-${res.locals.cspNonce}'`, XTERM_CDN];
    if (remoteEval) {
        scriptSources.push("'unsafe-eval'");
    }

    return helmet({
        contentSecurityPolicy: {
            directives: {
                defaultSrc: ["'self'"],
                scriptSrc: scriptSources,
                // Inline event handlers migrated to data-bd-call + bd-actions.js
                scriptSrcAttr: ["'none'"],
                styleSrc: ["'self'", "'unsafe-inline'", "https://fonts.googleapis.com", XTERM_CDN],
                fontSrc: ["'self'", "https://fonts.gstatic.com"],
                imgSrc: ["'self'", "data:", "blob:"],
                mediaSrc: ["'self'", "blob:"],
                // Allow WebSocket connections (ws:// or wss:// depending on mode)
                connectSrc: ["'self'", https ? 'wss:' : 'ws:', XTERM_CDN],
                frameSrc: ["'self'"],
                objectSrc: ["'none'"],
                childSrc: ["'self'"],
                workerSrc: ["'self'", "blob:"],
                baseUri: ["'self'"],
                formAction: ["'self'"],
                frameAncestors: ["'self'"],
                upgradeInsecureRequests: https ? [] : null
            }
        },
        crossOriginEmbedderPolicy: false,
        crossOriginResourcePolicy: { policy: 'same-origin' },
        crossOriginOpenerPolicy: https ? { policy: 'same-origin' } : false,
        originAgentCluster: https,
        strictTransportSecurity: hsts
            ? { maxAge: 31536000, includeSubDomains: true, preload: false }
            : false,
        dnsPrefetchControl: { allow: false },
        referrerPolicy: { policy: 'strict-origin-when-cross-origin' }
    });
}

// helmet() validates and compiles its whole configuration on each call, so
// building it per request cost every response that work. The inputs take a
// handful of values; build each combination once. The nonce stays per
// request through the function directive above.
const helmetByVariant = new Map();

function buildHelmetMiddleware(req, res) {
    res.locals.cspNonce = crypto.randomBytes(16).toString('base64');

    const variant = {
        remoteEval: req.path.startsWith('/remote') && allowRemoteUnsafeEval(),
        https: !!config.httpsEnabled,
        hsts: shouldSendStrictTransportSecurity()
    };
    const key = `${variant.remoteEval}|${variant.https}|${variant.hsts}`;
    let middleware = helmetByVariant.get(key);
    if (!middleware) {
        middleware = createHelmet(variant);
        helmetByVariant.set(key, middleware);
    }
    return middleware;
}

/**
 * Custom security headers beyond what Helmet provides
 */
function customSecurityHeaders(req, res, next) {
    // Prevent clickjacking (belt + suspenders with CSP frame-ancestors)
    res.setHeader('X-Frame-Options', 'SAMEORIGIN');

    // Prevent MIME type sniffing
    res.setHeader('X-Content-Type-Options', 'nosniff');

    // XSS Protection (disabled — CSP is the modern replacement)
    res.setHeader('X-XSS-Protection', '0');

    // Permissions policy — restrict powerful APIs
    // Only use standardized features; non-standard ones (browsing-topics,
    // attribution-reporting, private-state-token-*, etc.) cause console warnings.
    res.setHeader('Permissions-Policy',
        'geolocation=(), microphone=(self), camera=(), ' +
        'payment=(), usb=(), ' +
        'accelerometer=(), gyroscope=(), magnetometer=()');

    // Prevent cross-site leak via cache timing
    res.setHeader('Cache-Control', 'no-store');
    // Allow static assets to be cached (overridden in express.static options)
    if (req.path.startsWith('/css/') || req.path.startsWith('/js/') ||
        req.path.startsWith('/img/') || req.path.startsWith('/fonts/')) {
        res.setHeader('Cache-Control', 'public, max-age=3600');
    }

    next();
}

/**
 * Combined security middleware
 */
function securityMiddleware(req, res, next) {
    buildHelmetMiddleware(req, res)(req, res, () => {
        customSecurityHeaders(req, res, next);
    });
}

module.exports = securityMiddleware;
module.exports.allowRemoteUnsafeEval = allowRemoteUnsafeEval;
