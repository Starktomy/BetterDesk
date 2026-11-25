/**
 * /uploads serves branding images only, under a sandboxing CSP.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const express = require('express');
const request = require('supertest');
const { createUploadsStatic, UPLOADS_CSP } = require('../lib/uploadsStatic');

describe('createUploadsStatic', () => {
    let dir;
    let app;

    beforeAll(() => {
        dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-uploads-'));
        fs.writeFileSync(path.join(dir, 'logo-0123456789abcdef.svg'), '<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>');
        fs.writeFileSync(path.join(dir, 'logo-0123456789abcdef.png'), 'png');
        fs.writeFileSync(path.join(dir, 'logo-0123456789abcdef.html'), '<script>alert(1)</script>');
        fs.writeFileSync(path.join(dir, 'logo-0123456789abcdef.js'), 'alert(1)');
        fs.writeFileSync(path.join(dir, '.secret.png'), 'hidden');
        app = express();
        app.use('/uploads', createUploadsStatic(dir));
    });

    afterAll(() => {
        fs.rmSync(dir, { recursive: true, force: true });
    });

    it('serves images with a sandboxing CSP', async () => {
        for (const name of ['logo-0123456789abcdef.svg', 'logo-0123456789abcdef.png']) {
            const res = await request(app).get(`/uploads/${name}`);
            expect(res.status).toBe(200);
            expect(res.headers['content-security-policy']).toBe(UPLOADS_CSP);
            expect(res.headers['x-content-type-options']).toBe('nosniff');
        }
        const svg = await request(app).get('/uploads/logo-0123456789abcdef.svg');
        expect(svg.headers['content-type']).toMatch(/^image\/svg\+xml/);
        expect(UPLOADS_CSP).toMatch(/\bsandbox\b/);
    });

    it('refuses markup, scripts, dotfiles and nested paths', async () => {
        for (const probe of [
            'logo-0123456789abcdef.html',
            'logo-0123456789abcdef.js',
            '.secret.png',
            'x/logo-0123456789abcdef.png',
            'logo-0123456789abcdef.png%00.html',
            '..%2Flogo-0123456789abcdef.png',
        ]) {
            const res = await request(app).get(`/uploads/${probe}`);
            expect([probe, res.status]).toEqual([probe, 404]);
        }
    });
});
