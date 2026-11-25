/**
 * Client addresses come from the trust proxy setting, never from a client's
 * own X-Forwarded-For.
 */

const http = require('http');
const express = require('express');
const request = require('supertest');

function rawRequest(socketAddr, forwardedFor) {
    return {
        socket: { remoteAddress: socketAddr },
        headers: forwardedFor ? { 'x-forwarded-for': forwardedFor } : {},
    };
}

function withTrustProxy(value, fn) {
    const original = process.env.TRUST_PROXY;
    if (value === undefined) delete process.env.TRUST_PROXY;
    else process.env.TRUST_PROXY = value;
    jest.isolateModules(() => fn(require('../lib/clientIp')));
    if (original === undefined) delete process.env.TRUST_PROXY;
    else process.env.TRUST_PROXY = original;
}

describe('clientIp for raw upgrade requests', () => {
    it('ignores X-Forwarded-For when no proxy is trusted', () => {
        withTrustProxy(undefined, ({ clientIpFromRaw }) => {
            expect(clientIpFromRaw(rawRequest('203.0.113.5', '6.6.6.6'))).toBe('203.0.113.5');
        });
    });

    it('takes the hop added by a trusted proxy, not one the client sent', () => {
        withTrustProxy('loopback', ({ clientIpFromRaw }) => {
            expect(clientIpFromRaw(rawRequest('127.0.0.1', '198.51.100.7'))).toBe('198.51.100.7');
            // The proxy appended the real address after the client's forgery.
            expect(clientIpFromRaw(rawRequest('127.0.0.1', '6.6.6.6, 198.51.100.7'))).toBe('198.51.100.7');
            // A direct connection is never trusted to name someone else.
            expect(clientIpFromRaw(rawRequest('203.0.113.5', '6.6.6.6'))).toBe('203.0.113.5');
        });
    });
});

describe('goApiProxy forwarding', () => {
    let target;
    let seen;
    let config;
    let originalUrl;

    beforeAll(async () => {
        target = http.createServer((req, res) => {
            seen = req.headers;
            res.end('{}');
        });
        await new Promise((resolve) => target.listen(0, '127.0.0.1', resolve));
        config = require('../config/config');
        originalUrl = config.betterdeskApiUrl;
        config.betterdeskApiUrl = `http://127.0.0.1:${target.address().port}/api`;
    });

    afterAll(async () => {
        config.betterdeskApiUrl = originalUrl;
        await new Promise((resolve) => target.close(resolve));
    });

    it('sends Go the resolved address instead of the client headers', async () => {
        const { goApiProxy } = require('../middleware/goApiProxy');
        const app = express();
        app.set('trust proxy', false);
        app.use(goApiProxy);

        await request(app)
            .get('/api/login-options')
            .set('X-Forwarded-For', '6.6.6.6')
            .set('X-Real-IP', '7.7.7.7');

        expect(seen['x-real-ip']).toMatch(/127\.0\.0\.1$/);
        expect(seen['x-forwarded-for']).toBe(seen['x-real-ip']);
        expect(`${seen['x-forwarded-for']} ${seen['x-real-ip']}`).not.toMatch(/6\.6\.6\.6|7\.7\.7\.7/);
    });
});
