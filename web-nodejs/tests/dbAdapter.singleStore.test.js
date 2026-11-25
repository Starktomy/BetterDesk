'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');
const Database = require('better-sqlite3');

describe('dbAdapter SQLite single-store topology', () => {
    let tempDir;

    beforeEach(() => {
        tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'betterdesk-single-store-'));
        process.env.DATA_DIR = path.join(tempDir, 'panel-data');
        process.env.DB_PATH = path.join(tempDir, 'db_v2.sqlite3');
        process.env.DB_TYPE = 'sqlite';
        jest.resetModules();
    });

    afterEach(() => {
        delete process.env.DATA_DIR;
        delete process.env.DB_PATH;
        delete process.env.DB_TYPE;
        fs.rmSync(tempDir, { recursive: true, force: true });
    });

    it('uses db_v2.sqlite3 on a fresh install and never creates auth.db', async () => {
        const { getAdapter } = require('../services/dbAdapter');
        const adapter = getAdapter();
        await adapter.init();

        const legacyAuthPath = path.join(process.env.DATA_DIR, 'auth.db');
        expect(fs.existsSync(legacyAuthPath)).toBe(false);

        const main = new Database(process.env.DB_PATH, { readonly: true });
        expect(main.prepare(`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'users'`).get()).toBeTruthy();
        main.close();
        expect(adapter.getSqliteAuthDb()).toBe(adapter.getSqliteMainDb());
        await adapter.close();
    });

    it('caps ticket and registration lists only when asked', async () => {
        const { getAdapter } = require('../services/dbAdapter');
        const adapter = getAdapter();
        await adapter.init();
        try {
            for (let i = 0; i < 5; i += 1) {
                await adapter.createTicket({ title: `t${i}`, description: '', priority: 'low', category: 'general', createdBy: 'ops' });
                await adapter.createPendingRegistration({ device_id: `dev${i}` });
            }
            expect(await adapter.getAllTickets({})).toHaveLength(5);
            expect(await adapter.getAllTickets({ limit: 2 })).toHaveLength(2);
            expect(await adapter.getAllTickets({ status: 'open', limit: 3 })).toHaveLength(3);
            expect(await adapter.getAllTickets({ limit: 'abc' })).toHaveLength(5);
            expect(await adapter.getPendingRegistrations({ status: 'pending' })).toHaveLength(5);
            expect(await adapter.getPendingRegistrations({ status: 'pending', limit: 3 })).toHaveLength(3);
        } finally {
            await adapter.close();
        }
    });

    it('loads group memberships for the requested peers in name order', async () => {
        const { getAdapter } = require('../services/dbAdapter');
        const adapter = getAdapter();
        await adapter.init();
        try {
            const west = await adapter.createDeviceGroup({ name: 'West' });
            const east = await adapter.createDeviceGroup({ name: 'East' });
            await adapter.addDeviceToGroup(west.guid, 'peer-a');
            await adapter.addDeviceToGroup(east.guid, 'peer-a');
            await adapter.addDeviceToGroup(east.guid, 'peer-b');

            const groups = await adapter.getDeviceGroupsForPeers(['peer-a', 'empty']);
            expect([...groups.keys()]).toEqual(['peer-a']);
            expect(groups.get('peer-a')).toEqual(await adapter.getDeviceGroupsForPeer('peer-a'));
            expect(groups.get('peer-a').map(group => group.name)).toEqual(['East', 'West']);
            await expect(adapter.getDeviceGroupsForPeers([])).resolves.toEqual(new Map());
        } finally {
            await adapter.close();
        }
    });
});
