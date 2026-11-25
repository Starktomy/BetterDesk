/**
 * Chat attachment access: whole-ID lookup and the chat.access permission.
 */

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const request = require('supertest');
const { createTestApp, withAuth } = require('./helpers');

const UPLOAD_DIR = path.join(__dirname, '..', 'data', 'chat-files');

function appFor(role) {
    const app = createTestApp();
    withAuth(app, { role });
    app.use(require('../routes/chat.routes'));
    return app;
}

function storeFile(name, contents) {
    fs.mkdirSync(UPLOAD_DIR, { recursive: true });
    fs.writeFileSync(path.join(UPLOAD_DIR, name), contents);
    created.push(name);
}

const created = [];

afterEach(() => {
    for (const name of created.splice(0)) {
        fs.rmSync(path.join(UPLOAD_DIR, name), { force: true });
    }
});

describe('Chat attachments', () => {
    const id = crypto.randomBytes(16).toString('hex');

    it('serves a file only for its whole stored name', async () => {
        storeFile(`${id}.txt`, 'secret');
        const app = appFor('viewer');

        const res = await request(app).get(`/api/chat/files/${id}.txt`);
        expect(res.status).toBe(200);
        expect(res.text).toBe('secret');

        for (const probe of [id[0], id.slice(0, 31), `${id}.TXT`, `${id}.t%2Fxt`]) {
            const miss = await request(app).get(`/api/chat/files/${probe}`);
            expect(miss.status).toBe(404);
        }
    });

    it('still resolves legacy links that carry only the ID', async () => {
        storeFile(`${id}.pdf`, 'legacy');
        const res = await request(appFor('viewer'))
            .get(`/api/chat/files/${id}`)
            .buffer(true)
            .parse((response, done) => {
                const chunks = [];
                response.on('data', (chunk) => chunks.push(chunk));
                response.on('end', () => done(null, Buffer.concat(chunks)));
            });
        expect(res.status).toBe(200);
        expect(res.body.toString()).toBe('legacy');
    });

    it('requires chat.access to download, upload or search', async () => {
        storeFile(`${id}.txt`, 'secret');
        const app = appFor('guest_issuer');

        expect((await request(app).get(`/api/chat/files/${id}.txt`)).status).toBe(403);
        expect((await request(app).get('/api/chat/search?q=x')).status).toBe(403);
        const upload = await request(app).post('/api/chat/upload').attach('file', Buffer.from('x'), 'a.txt');
        expect(upload.status).toBe(403);
        expect(fs.readdirSync(UPLOAD_DIR).filter((name) => !created.includes(name))).toEqual([]);
    });

    it('stores uploads under a random ID with a sanitized extension', async () => {
        const app = appFor('viewer');
        const res = await request(app).post('/api/chat/upload').attach('file', Buffer.from('x'), 'report.HTML');
        expect(res.status).toBe(200);
        created.push(res.body.filename);
        expect(res.body.filename).toMatch(/^[a-f0-9]{32}\.html$/);
        expect(res.body.url).toBe(`/api/chat/files/${res.body.filename}`);

        const odd = await request(app).post('/api/chat/upload').attach('file', Buffer.from('x'), 'x.p%20f');
        created.push(odd.body.filename);
        expect(odd.body.filename).toMatch(/^[a-f0-9]{32}$/);
    });
});
