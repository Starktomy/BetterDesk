/**
 * Server terminal WebSocket: who may open a shell, and what ends one.
 */

const http = require('http');
const WebSocket = require('ws');

const mockPermissions = new Map();
jest.mock('../services/rolePermissionService', () => ({
    getEffectivePermissions: jest.fn(async (role) => mockPermissions.get(role) || new Set()),
    loadEffectivePermissions: jest.fn(),
}));

// A fake PTY so the tests never start a real shell on the test machine.
const mockPty = {
    write: jest.fn(),
    resize: jest.fn(),
    onData: jest.fn(),
    onExit: jest.fn(),
    kill: jest.fn(),
};
jest.mock('node-pty', () => ({ spawn: jest.fn(() => mockPty) }));

const pty = require('node-pty');
const { initServerTerminalProxy } = require('../services/serverTerminalProxy');

function startServer(state, opts = {}) {
    const server = http.createServer();
    const sessionMiddleware = (req, _res, next) => {
        req.session = {
            userId: 7,
            user: { id: 7, username: 'ops', role: state.role },
            cookie: {},
            createdAt: Date.now(),
            lastActivityAt: Date.now(),
            reload(fn) {
                if (state.destroyed) return fn(new Error('failed to load session'));
                return fn();
            },
            save(fn) {
                state.saved += 1;
                if (fn) fn();
            },
        };
        next();
    };
    initServerTerminalProxy(server, sessionMiddleware, {
        getUserById: async () => ({ id: 7, role: state.dbRole || state.role }),
        revalidateMs: 30,
        ...opts,
    });
    return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server)));
}

function connect(server) {
    const { port } = server.address();
    return new WebSocket(`ws://127.0.0.1:${port}/ws/server-management/terminal`);
}

function rejection(ws) {
    return new Promise((resolve) => {
        ws.on('unexpected-response', (_req, res) => resolve(res.statusCode));
        ws.on('open', () => resolve('open'));
        ws.on('error', () => {});
    });
}

function endReason(ws) {
    return new Promise((resolve, reject) => {
        let reason = null;
        ws.on('message', (raw) => {
            const msg = JSON.parse(raw.toString());
            if (msg.type === 'end') reason = msg.reason;
        });
        ws.on('close', () => resolve(reason));
        ws.on('error', reject);
    });
}

describe('server terminal proxy', () => {
    let server;

    beforeEach(() => {
        mockPermissions.clear();
        mockPermissions.set('server_admin', new Set(['server.config']));
    });

    afterEach(async () => {
        if (server) await new Promise((resolve) => server.close(resolve));
        server = null;
    });

    it('rejects roles outside the terminal list', async () => {
        server = await startServer({ role: 'operator', saved: 0 });
        expect(await rejection(connect(server))).toBe(403);
    });

    it('rejects a terminal role whose server.config permission was removed', async () => {
        mockPermissions.set('server_admin', new Set());
        server = await startServer({ role: 'server_admin', saved: 0 });
        expect(await rejection(connect(server))).toBe(403);
    });

    it('ends an open shell when the session is destroyed', async () => {
        const state = { role: 'super_admin', saved: 0 };
        server = await startServer(state);
        const ws = connect(server);
        await new Promise((resolve) => ws.on('open', resolve));
        const ended = endReason(ws);
        state.destroyed = true;
        expect(await ended).toBe('session_ended');
    });

    it('ends an open shell when the user is demoted', async () => {
        const state = { role: 'server_admin', saved: 0 };
        server = await startServer(state);
        const ws = connect(server);
        await new Promise((resolve) => ws.on('open', resolve));
        const ended = endReason(ws);
        state.dbRole = 'operator';
        expect(await ended).toBe('forbidden');
    });

    it('ends a shell with no input after the idle timeout', async () => {
        server = await startServer({ role: 'super_admin', saved: 0 }, { idleMs: 80, revalidateMs: 10_000 });
        const ws = connect(server);
        expect(await endReason(ws)).toBe('idle');
    });

    it('refreshes the panel session while the operator is typing', async () => {
        const state = { role: 'super_admin', saved: 0 };
        server = await startServer(state);
        const ws = connect(server);
        await new Promise((resolve) => ws.on('open', resolve));
        ws.send(JSON.stringify({ type: 'init', cols: 80, rows: 24 }));
        ws.send(JSON.stringify({ type: 'input', data: 'ls\r' }));
        await new Promise((resolve) => setTimeout(resolve, 100));
        const closed = new Promise((resolve) => ws.on('close', resolve));
        ws.close();
        await closed;
        expect(state.saved).toBeGreaterThan(0);
        expect(mockPty.write).toHaveBeenCalledWith('ls\r');
    });

    it('spawns one shell per connection and kills it on close', async () => {
        pty.spawn.mockClear();
        mockPty.kill.mockClear();
        server = await startServer({ role: 'super_admin', saved: 0 });
        const ws = connect(server);
        await new Promise((resolve) => ws.on('open', resolve));
        ws.send(JSON.stringify({ type: 'init' }));
        ws.send(JSON.stringify({ type: 'init' }));
        await new Promise((resolve) => setTimeout(resolve, 50));
        ws.close();
        await new Promise((resolve) => setTimeout(resolve, 50));
        expect(pty.spawn).toHaveBeenCalledTimes(1);
        expect(mockPty.kill).toHaveBeenCalled();
    });
});
