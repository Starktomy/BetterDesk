/**
 * List endpoints return a bounded page and say when more exist; the
 * database stats widget shares one result between polls.
 */

const request = require('supertest');
const { createTestApp, withAuth } = require('./helpers');

const mockAdapter = {
    getAllTickets: jest.fn(async (filters) => Array.from({ length: filters.limit || 3 }, (_, id) => ({ id }))),
    allAsync: jest.fn(async () => [{ name: 'peers' }]),
    getAsync: jest.fn(async () => ({ count: 7 })),
};
jest.mock('../services/dbAdapter', () => ({
    getAdapter: () => mockAdapter,
    allAsync: (...args) => mockAdapter.allAsync(...args),
    getAsync: (...args) => mockAdapter.getAsync(...args),
}));

function appWith(router, mount = '/') {
    const app = createTestApp();
    withAuth(app, { role: 'admin', username: 'admin' });
    app.use(mount, router);
    return app;
}

describe('ticket list cap', () => {
    it('returns the newest 1000 tickets and flags the rest', async () => {
        const app = appWith(require('../routes/tickets.routes'), '/api/tickets');
        const res = await request(app).get('/api/tickets');

        expect(res.status).toBe(200);
        expect(mockAdapter.getAllTickets).toHaveBeenCalledWith(expect.objectContaining({ limit: 1001 }));
        expect(res.body.tickets).toHaveLength(1000);
        expect(res.body.truncated).toBe(true);
    });
});

describe('database stats widget', () => {
    it('counts tables once for polls within the cache window', async () => {
        const app = appWith(require('../routes/system.routes'));
        const [first, second] = await Promise.all([
            request(app).get('/api/database/stats'),
            request(app).get('/api/database/stats'),
        ]);
        const third = await request(app).get('/api/database/stats');

        for (const res of [first, second, third]) {
            expect(res.status).toBe(200);
            expect(res.body.tables).toEqual([{ name: 'peers', count: 7 }]);
        }
        expect(mockAdapter.allAsync).toHaveBeenCalledTimes(1);
        expect(mockAdapter.getAsync).toHaveBeenCalledTimes(1);
    });
});
