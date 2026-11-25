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

    it('refuses uploads before writing when the disk is nearly full', async () => {
        const statfs = jest.spyOn(fs.promises, 'statfs').mockResolvedValue({ bavail: 10, bsize: 4096 });
        try {
            const before = fs.readdirSync(UPLOAD_DIR).length;
            const res = await request(appFor('viewer')).post('/api/chat/upload').attach('file', Buffer.from('x'), 'a.txt');
            expect(res.status).toBe(507);
            expect(fs.readdirSync(UPLOAD_DIR).length).toBe(before);
        } finally {
            statfs.mockRestore();
        }
    });

    it('counts uploads still in progress against the free space', async () => {
        const http = require('http');
        const reserve = 1024 * 1024 * 1024 + 50 * 1024 * 1024; // the reserve plus one upload
        const statfs = jest.spyOn(fs.promises, 'statfs').mockResolvedValue({ bavail: reserve + 1024, bsize: 1 });
        const server = http.createServer(appFor('viewer'));
        await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
        const port = server.address().port;
        try {
            // Hold the first upload open mid-body.
            const boundary = 'bdtestboundary';
            const slow = http.request({
                host: '127.0.0.1', port, method: 'POST', path: '/api/chat/upload',
                headers: { 'Content-Type': 'multipart/form-data; boundary=' + boundary },
            });
            const slowDone = new Promise((resolve) => slow.on('response', (res) => {
                let body = '';
                res.on('data', (c) => { body += c; });
                res.on('end', () => resolve({ status: res.statusCode, body }));
            }));
            slow.write('--' + boundary + '\r\nContent-Disposition: form-data; name="file"; filename="a.txt"\r\n\r\n');
            await new Promise((resolve) => setTimeout(resolve, 100));

            const second = await request(server).post('/api/chat/upload').attach('file', Buffer.from('x'), 'b.txt');
            expect(second.status).toBe(507);

            slow.end('data\r\n--' + boundary + '--\r\n');
            const first = await slowDone;
            expect(first.status).toBe(200);
            created.push(JSON.parse(first.body).filename);

            // The reservation is released once the first upload ends.
            await new Promise((resolve) => setTimeout(resolve, 50));
            const third = await request(server).post('/api/chat/upload').attach('file', Buffer.from('x'), 'c.txt');
            expect(third.status).toBe(200);
            created.push(third.body.filename);
        } finally {
            statfs.mockRestore();
            await new Promise((resolve) => server.close(resolve));
        }
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
