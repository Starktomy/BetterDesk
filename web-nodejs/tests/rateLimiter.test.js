/**
 * Panel poll rate-limit classification tests
 */

const {
    isPanelPollRequest,
    isPanelReadRequest,
    isPanelPreferenceWrite,
    resolveApiPath,
    PANEL_POLL_PATHS,
    PANEL_READ_PATHS,
    meshActionLimiter
} = require('../middleware/rateLimiter');

describe('rateLimiter panel poll paths', () => {
    it('classifies dashboard client-config GET as panel poll', () => {
        expect(isPanelPollRequest({ method: 'GET', path: '/api/dashboard/client-config' })).toBe(true);
    });

    it('does not classify POST mutations as panel poll', () => {
        expect(isPanelPollRequest({ method: 'POST', path: '/api/devices' })).toBe(false);
        expect(isPanelPollRequest({ method: 'POST', path: '/api/dashboard/client-config' })).toBe(false);
    });

    it('includes core widget paths in the poll set', () => {
        expect(PANEL_POLL_PATHS.has('/api/stats')).toBe(true);
        expect(PANEL_POLL_PATHS.has('/api/dashboard/activity')).toBe(true);
    });

    it('includes devices page read paths in the poll set', () => {
        expect(PANEL_POLL_PATHS.has('/api/folders')).toBe(true);
        expect(PANEL_POLL_PATHS.has('/api/tags')).toBe(true);
        expect(PANEL_POLL_PATHS.has('/api/device-groups')).toBe(true);
        expect(PANEL_POLL_PATHS.has('/api/bd/notifications')).toBe(true);
    });

    it('classifies lightweight settings reads separately from mutations', () => {
        expect(PANEL_READ_PATHS.has('/api/settings/branding')).toBe(true);
        expect(isPanelReadRequest({ method: 'GET', path: '/api/settings/branding' })).toBe(true);
        expect(isPanelReadRequest({ method: 'HEAD', path: '/api/settings/info' })).toBe(true);
        expect(isPanelReadRequest({ method: 'POST', path: '/api/settings/branding' })).toBe(false);
        expect(isPanelReadRequest({ method: 'GET', path: '/api/settings/backup' })).toBe(false);
    });

    it('classifies /api/panel/* GET as panel poll via prefix', () => {
        expect(isPanelPollRequest({ method: 'GET', path: '/api/panel/strategies' })).toBe(true);
        expect(isPanelPollRequest({ method: 'GET', path: '/api/panel/user-groups' })).toBe(true);
    });

    it('does not classify unrelated API paths as panel poll', () => {
        expect(isPanelPollRequest({ method: 'GET', path: '/api/settings/info' })).toBe(false);
    });

    it('classifies desktop layout POST as panel preference write', () => {
        expect(isPanelPreferenceWrite({ method: 'POST', path: '/api/desktop/layout' })).toBe(true);
        expect(isPanelPreferenceWrite({ method: 'GET', path: '/api/desktop/layout' })).toBe(false);
        expect(isPanelPreferenceWrite({ method: 'POST', path: '/api/devices' })).toBe(false);
    });

    it('rejoins /api mount prefix when Express strips it from req.path', () => {
        // app.use('/api/', apiLimiter) → path=/bd/notifications, baseUrl=/api
        expect(resolveApiPath({
            path: '/bd/notifications',
            baseUrl: '/api',
            originalUrl: '/api/bd/notifications?limit=10'
        })).toBe('/api/bd/notifications');

        expect(isPanelPollRequest({
            method: 'GET',
            path: '/bd/notifications',
            baseUrl: '/api',
            originalUrl: '/api/bd/notifications?limit=10'
        })).toBe(true);

        expect(isPanelPollRequest({
            method: 'GET',
            path: '/registrations/count',
            baseUrl: '/api',
            originalUrl: '/api/registrations/count'
        })).toBe(true);

        expect(isPanelPreferenceWrite({
            method: 'POST',
            path: '/desktop/layout',
            baseUrl: '/api',
            originalUrl: '/api/desktop/layout'
        })).toBe(true);

        expect(isPanelReadRequest({
            method: 'GET',
            path: '/settings/branding',
            baseUrl: '/api',
            originalUrl: '/api/settings/branding'
        })).toBe(true);
    });
});

describe('MeshCentral action rate limiter', () => {
    it('exports a dedicated limiter for remote device actions', () => {
        expect(meshActionLimiter).toEqual(expect.any(Function));
    });

    function meshApp(maxEnv) {
        const express = require('express');
        const original = process.env.MESH_ACTION_RATE_LIMIT_MAX;
        process.env.MESH_ACTION_RATE_LIMIT_MAX = maxEnv;
        let limiter;
        jest.isolateModules(() => {
            limiter = require('../middleware/rateLimiter').meshActionLimiter;
        });
        if (original === undefined) delete process.env.MESH_ACTION_RATE_LIMIT_MAX;
        else process.env.MESH_ACTION_RATE_LIMIT_MAX = original;
        const app = express();
        app.use((req, _res, next) => { req.session = { userId: 9 }; next(); });
        app.post('/api/mesh/devices/:id/:action', limiter, (_req, res) => res.json({ ok: true }));
        return app;
    }

    it('keeps a separate budget per action kind', async () => {
        const supertest = require('supertest');
        const app = meshApp('2');
        for (let i = 0; i < 2; i += 1) {
            expect((await supertest(app).post('/api/mesh/devices/1/terminal')).status).toBe(200);
        }
        expect((await supertest(app).post('/api/mesh/devices/1/terminal')).status).toBe(429);
        expect((await supertest(app).post('/api/mesh/devices/1/power')).status).toBe(200);
    });

    it('treats 0 as off instead of blocking every request', async () => {
        const supertest = require('supertest');
        const app = meshApp('0');
        for (let i = 0; i < 5; i += 1) {
            expect((await supertest(app).post('/api/mesh/devices/1/exec')).status).toBe(200);
        }
    });
});
