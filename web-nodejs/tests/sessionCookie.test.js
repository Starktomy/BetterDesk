/**
 * With HTTPS configured, the session cookie must still be issued when the
 * server falls back to plain HTTP, and stay Secure over HTTPS.
 */

const express = require('express');
const session = require('express-session');
const request = require('supertest');
const { sessionCookieOptions } = require('../lib/sessionCookie');

function appWith(cookie) {
    const app = express();
    app.set('trust proxy', 'loopback');
    app.use(session({ secret: 'test', resave: false, saveUninitialized: false, cookie }));
    app.get('/login', (req, res) => {
        req.session.userId = 1;
        res.send('ok');
    });
    return app;
}

describe('session cookie options', () => {
    it('issues the cookie on the HTTP fallback when HTTPS is configured', async () => {
        const res = await request(appWith(sessionCookieOptions({ httpsEnabled: true, maxAge: 60000 }))).get('/login');
        const cookie = (res.headers['set-cookie'] || []).join(';');
        expect(cookie).toMatch(/connect\.sid=/);
        expect(cookie).not.toMatch(/Secure/i);
    });

    it('keeps the cookie Secure on HTTPS connections', async () => {
        const res = await request(appWith(sessionCookieOptions({ httpsEnabled: true, maxAge: 60000 })))
            .get('/login')
            .set('X-Forwarded-Proto', 'https');
        expect((res.headers['set-cookie'] || []).join(';')).toMatch(/Secure/i);
    });

    it('was never issued over HTTP with the old fixed secure flag', async () => {
        const res = await request(appWith({ secure: true, httpOnly: true, sameSite: 'lax', maxAge: 60000 })).get('/login');
        expect(res.headers['set-cookie']).toBeUndefined();
    });
});
