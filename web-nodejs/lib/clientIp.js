'use strict';

/**
 * Client address for rate limits, lockouts and audit records.
 *
 * X-Forwarded-For and X-Real-IP are sent by the client, so they are only
 * believed for hops the TRUST_PROXY setting trusts. Express requests already
 * resolve that as req.ip. WebSocket upgrades arrive as raw requests without
 * req.ip, so they are resolved here with the same trust function.
 */

const express = require('express');
const { parseTrustProxy } = require('./parseTrustProxy');

let trustFn = null;

function getTrustFn() {
    if (!trustFn) {
        // Compile the setting exactly as the console app does.
        const app = express();
        app.set('trust proxy', parseTrustProxy(process.env.TRUST_PROXY));
        trustFn = app.get('trust proxy fn');
    }
    return trustFn;
}

/**
 * Walk the forwarding chain from the socket outwards while each hop is a
 * trusted proxy; the first untrusted address is the client (proxy-addr rule).
 */
function clientIpFromRaw(req) {
    const socketAddr = req?.socket?.remoteAddress || '';
    const forwarded = String(req?.headers?.['x-forwarded-for'] || '')
        .split(',')
        .map((part) => part.trim())
        .filter(Boolean)
        .reverse();
    const chain = [socketAddr, ...forwarded];
    const trust = getTrustFn();
    let i = 0;
    while (i < chain.length - 1 && trust(chain[i], i)) i += 1;
    return chain[i] || 'unknown';
}

function clientIp(req) {
    if (req && typeof req.ip === 'string' && req.ip) return req.ip;
    return clientIpFromRaw(req);
}

function resetTrustCache() {
    trustFn = null;
}

module.exports = { clientIp, clientIpFromRaw, resetTrustCache };
