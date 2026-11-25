/**
 * Ending a user's sessions ends every active panel session (except the one
 * kept for the acting administrator) and revokes desktop tokens.
 */

const mockDb = { revokeAllUserTokens: jest.fn().mockResolvedValue(undefined) };
jest.mock('../services/database', () => mockDb);

const panelSessionService = require('../services/panelSessionService');
const { revokeUserSessionsAndTokens } = require('../services/authService');

describe('revokeUserSessionsAndTokens', () => {
    it('ends the user sessions, keeps the excepted one and revokes tokens', async () => {
        const ended = [];
        const store = {
            listUserSessions: jest.fn(async (userId, opts) => {
                expect(userId).toBe(5);
                expect(opts.includeEnded).toBe(false);
                return [{ sid: 'a' }, { sid: 'mine' }, { sid: 'b' }];
            }),
            endSession: jest.fn(async (sid, reason) => ended.push([sid, reason])),
        };
        panelSessionService.attachStore(store);

        const count = await revokeUserSessionsAndTokens(5, { exceptSid: 'mine' });

        expect(count).toBe(2);
        expect(ended).toEqual([['a', 'revoked'], ['b', 'revoked']]);
        expect(mockDb.revokeAllUserTokens).toHaveBeenCalledWith(5);
    });

    it('leaves a revoked session unusable in the real SQLite store', async () => {
        const fs = require('fs');
        const os = require('os');
        const path = require('path');
        const { DatabaseSessionStore } = require('../services/databaseSessionStore');
        const invoke = (method, ...args) => new Promise((resolve, reject) => {
            method(...args, (err, value) => (err ? reject(err) : resolve(value)));
        });
        const previousDbType = process.env.DB_TYPE;
        process.env.DB_TYPE = 'sqlite';
        const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'betterdesk-revoke-'));
        const store = new DatabaseSessionStore({
            config: { dbPath: path.join(tempDir, 'db_v2.sqlite3') },
            ttlMs: 24 * 60 * 60 * 1000,
            idleTimeoutMs: 60_000,
            maxAgeMs: 24 * 60 * 60 * 1000,
        });
        try {
            await store.ready;
            const session = (role) => ({
                user: { id: 8, username: 'ops', role },
                userId: 8,
                createdAt: Date.now(),
                lastActivityAt: Date.now(),
                cookie: { expires: new Date(Date.now() + 60_000).toISOString() },
            });
            await invoke(store.set.bind(store), 'stale', session('super_admin'));
            await invoke(store.set.bind(store), 'kept', session('super_admin'));
            panelSessionService.attachStore(store);

            await revokeUserSessionsAndTokens(8, { exceptSid: 'kept' });

            const stale = await invoke(store.get.bind(store), 'stale');
            expect(stale?.userId).toBeUndefined();
            expect((await invoke(store.get.bind(store), 'kept')).userId).toBe(8);
        } finally {
            await store.close();
            fs.rmSync(tempDir, { recursive: true, force: true });
            if (previousDbType === undefined) delete process.env.DB_TYPE;
            else process.env.DB_TYPE = previousDbType;
        }
    });
});
