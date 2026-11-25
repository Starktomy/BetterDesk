/**
 * System widgets must not block the event loop, and the speed test must
 * stream exactly the requested size.
 */

const request = require('supertest');
const { createTestApp, withAuth } = require('./helpers');

jest.mock('child_process', () => ({
    exec: jest.fn((_cmd, _opts, cb) => setTimeout(() => cb(null, 'slow-output\n'), 300)),
}));

const { exec } = require('child_process');

function makeApp() {
    const app = createTestApp();
    withAuth(app, { role: 'admin' });
    app.use(require('../routes/system.routes'));
    return app;
}

describe('system routes', () => {
    it('serves other requests while a widget command is running', async () => {
        const app = makeApp();
        const order = [];
        const slow = request(app).get('/api/docker/containers').then((res) => {
            order.push('docker');
            return res;
        });
        const fast = request(app).get('/api/speed-test?size=16').then((res) => {
            order.push('speed-test');
            return res;
        });
        const [slowRes, fastRes] = await Promise.all([slow, fast]);

        expect(order).toEqual(['speed-test', 'docker']);
        expect(fastRes.status).toBe(200);
        expect(slowRes.body.containers[0].name).toBe('slow-output');
        expect(exec).toHaveBeenCalled();
    });

    it.each([1, 65536, 65537, 3 * 65536 + 123])('streams exactly %d bytes', async (size) => {
        const res = await request(makeApp())
            .get(`/api/speed-test?size=${size}`)
            .buffer(true)
            .parse((response, done) => {
                let total = 0;
                response.on('data', (chunk) => { total += chunk.length; });
                response.on('end', () => done(null, total));
            });
        expect(res.status).toBe(200);
        expect(res.headers['content-length']).toBe(String(size));
        expect(res.body).toBe(size);
    });

    it('still runs only allow-listed commands', async () => {
        const app = makeApp();
        const denied = await request(app).post('/api/system/exec').send({ command: 'cat /etc/shadow' });
        expect(denied.status).toBe(403);
        const allowed = await request(app).post('/api/system/exec').send({ command: 'uptime' });
        expect(allowed.status).toBe(200);
        expect(allowed.body.output).toBe('slow-output');
    });
});
