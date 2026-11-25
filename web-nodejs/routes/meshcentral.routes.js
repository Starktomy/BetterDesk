/**
 * MeshCentral compatibility routes — settings, .msh download, API proxy.
 */

const express = require('express');
const { pipeline } = require('stream/promises');
const router = express.Router();
const { requireAuth, requirePermission } = require('../middleware/auth');
const { rdClientPageLimiter, meshActionLimiter } = require('../middleware/rateLimiter');
const { proxyToGo } = require('../lib/goApiProxy');
const betterdeskApi = require('../services/betterdeskApi');
const db = require('../services/database');
const deviceGroupService = require('../services/deviceGroupService');
const { identityFromRequest, panelTrustHeaders, HEADER } = require('../services/panelTrust');

async function rejectIfMeshDeviceOutOfScope(req, res, deviceId) {
    const scope = await deviceGroupService.authorizeUserDeviceAccess(db, req.session.user, deviceId);
    if (scope.allowed) return false;
    res.status(404).json({ error: 'Device not found' });
    return true;
}

router.get('/api/mesh/status', requireAuth, async (req, res) => {
    try {
        const result = await betterdeskApi.getMeshStatus();
        res.json(result.data || result);
    } catch (err) {
        res.status(500).json({ enabled: false, error: err.message });
    }
});

router.get('/api/mesh/server-id', requireAuth, requirePermission('server.config'), async (req, res) => {
    return proxyToGo(betterdeskApi.apiClient, req, res, 'GET', '/mesh/server-id');
});

router.get('/api/mesh/groups', requireAuth, requirePermission('server.config'), async (req, res) => {
    return proxyToGo(betterdeskApi.apiClient, req, res, 'GET', '/mesh/groups');
});

router.post('/api/mesh/groups', requireAuth, requirePermission('server.config'), async (req, res) => {
    return proxyToGo(betterdeskApi.apiClient, req, res, 'POST', '/mesh/groups', req.body);
});

router.get('/api/mesh/download.msh', requireAuth, requirePermission('server.config'), async (req, res) => {
  try {
        const qs = new URLSearchParams(req.query).toString();
        const path = '/mesh/download.msh' + (qs ? `?${qs}` : '');
        const resp = await betterdeskApi.apiClient({
            method: 'GET',
            url: path,
            responseType: 'arraybuffer',
        });
        res.set('Content-Type', 'application/octet-stream');
        res.set('Content-Disposition', 'attachment; filename="meshagents.msh"');
        res.status(resp.status).send(Buffer.from(resp.data));
    } catch (err) {
        res.status(500).json({ error: err.message });
    }
});

router.post('/api/mesh/devices/:id/desktop', rdClientPageLimiter, async (req, res, next) => {
    const id = req.params.id;
    const meshShare = String(req.query.mesh_share || '').trim();
    const guest = String(req.query.guest || req.query.t || '').trim();

    // Guest / mesh_share: no panel session — validate then proxy with API key
    if (meshShare || guest) {
        try {
            if (meshShare) {
                const result = await betterdeskApi.apiClient.get('/mesh/share/validate', {
                    params: { token: meshShare, peer_id: id },
                });
                if (!result.data?.valid) {
                    return res.status(403).json({ error: 'Invalid or expired share link' });
                }
            } else if (guest) {
                const result = await betterdeskApi.apiClient.get('/guest/access-links/validate', {
                    params: { token: guest, peer_id: id },
                });
                if (!result.data?.valid) {
                    return res.status(403).json({ error: 'Invalid or expired guest link' });
                }
            }
        } catch {
            return res.status(403).json({ error: 'Invalid or expired share link' });
        }
        return proxyToGo(betterdeskApi.apiClient, req, res, 'POST', () => {
            const query = new URLSearchParams(req.query);
            if (guest) query.set('guest', guest);
            return `/mesh/devices/${encodeURIComponent(id)}/desktop?${query}`;
        });
    }

    return requireAuth(req, res, () => requirePermission('device.connect')(req, res, async () => {
        if (await rejectIfMeshDeviceOutOfScope(req, res, id)) return;
        return proxyToGo(betterdeskApi.apiClient, req, res, 'POST', () => `/mesh/devices/${encodeURIComponent(id)}/desktop`);
    }));
});

router.post('/api/mesh/devices/:id/terminal', meshActionLimiter, requireAuth, requirePermission('mesh.terminal'), async (req, res) => {
    const id = req.params.id;
    if (await rejectIfMeshDeviceOutOfScope(req, res, id)) return;
    return proxyToGo(betterdeskApi.apiClient, req, res, 'POST', () => `/mesh/devices/${encodeURIComponent(id)}/terminal`);
});

router.post('/api/mesh/devices/:id/files', meshActionLimiter, requireAuth, requirePermission('mesh.files'), async (req, res) => {
    const id = req.params.id;
    if (await rejectIfMeshDeviceOutOfScope(req, res, id)) return;
    return proxyToGo(betterdeskApi.apiClient, req, res, 'POST', () => `/mesh/devices/${encodeURIComponent(id)}/files`);
});

router.post('/api/mesh/devices/:id/share', meshActionLimiter, requireAuth, requirePermission('device.connect'), async (req, res) => {
    const id = req.params.id;
    if (await rejectIfMeshDeviceOutOfScope(req, res, id)) return;
    return proxyToGo(betterdeskApi.apiClient, req, res, 'POST', () => `/mesh/devices/${encodeURIComponent(id)}/share`, req.body);
});

router.get('/api/mesh/share/validate', rdClientPageLimiter, async (req, res) => {
    return proxyToGo(betterdeskApi.apiClient, req, res, 'GET', () => {
        const qs = new URLSearchParams(req.query).toString();
        return '/mesh/share/validate' + (qs ? `?${qs}` : '');
    });
});

router.post('/api/mesh/devices/:id/tcp', meshActionLimiter, requireAuth, requirePermission('device.connect'), async (req, res) => {
    const id = req.params.id;
    if (await rejectIfMeshDeviceOutOfScope(req, res, id)) return;
    return proxyToGo(betterdeskApi.apiClient, req, res, 'POST', () => `/mesh/devices/${encodeURIComponent(id)}/tcp`, req.body);
});

router.post('/api/mesh/devices/:id/udp', meshActionLimiter, requireAuth, requirePermission('device.connect'), async (req, res) => {
    const id = req.params.id;
    if (await rejectIfMeshDeviceOutOfScope(req, res, id)) return;
    return proxyToGo(betterdeskApi.apiClient, req, res, 'POST', () => `/mesh/devices/${encodeURIComponent(id)}/udp`, req.body);
});

router.post('/api/mesh/devices/:id/power', meshActionLimiter, requireAuth, requirePermission('mesh.power'), async (req, res) => {
    const id = req.params.id;
    if (await rejectIfMeshDeviceOutOfScope(req, res, id)) return;
    return proxyToGo(betterdeskApi.apiClient, req, res, 'POST', () => `/mesh/devices/${encodeURIComponent(id)}/power`, req.body);
});

router.post('/api/mesh/devices/:id/group', requireAuth, requirePermission('server.config'), async (req, res) => {
    const id = req.params.id;
    return proxyToGo(betterdeskApi.apiClient, req, res, 'POST', () => `/mesh/devices/${encodeURIComponent(id)}/group`, req.body);
});

async function proxyMeshRecordings(req, res, path, download = false) {
    const headers = panelTrustHeaders(identityFromRequest(req), 'GET', `/api${path}`);
    if (!headers[HEADER]) {
        return res.status(503).json({ error: 'Panel trust bridge is unavailable' });
    }
    if (download && req.headers.range) headers.Range = req.headers.range;
    try {
        const response = await betterdeskApi.apiClient.get(path, { headers, responseType: download ? 'stream' : 'json' });
        if (!download) return res.status(response.status).json(response.data);
        for (const name of ['content-type', 'content-length', 'content-range', 'accept-ranges']) {
            if (response.headers[name]) res.set(name, response.headers[name]);
        }
        res.status(response.status);
        await pipeline(response.data, res);
    } catch (err) {
        if (download) err.response?.data?.destroy?.();
        if (!res.headersSent && !res.destroyed) {
            res.status(err.response?.status || 502).json({ error: 'Could not read mesh recordings' });
        }
    }
}

router.get('/api/mesh/recordings', requireAuth, requirePermission('device.view'), async (req, res) => {
    return proxyMeshRecordings(req, res, '/mesh/recordings');
});

router.get('/api/mesh/recordings/:id', requireAuth, requirePermission('device.connect'), async (req, res) => {
    const id = req.params.id;
    if (!/^[A-Za-z0-9_-]+\.mcrec$/i.test(id)) {
        return res.status(404).json({ error: 'Recording not found' });
    }
    return proxyMeshRecordings(req, res, `/mesh/recordings/${encodeURIComponent(id)}`, true);
});

router.get('/api/session/recordings', requireAuth, requirePermission('device.view'), async (req, res) => {
    return proxyMeshRecordings(req, res, '/session/recordings');
});

router.post('/api/mesh/devices/:id/exec', meshActionLimiter, requireAuth, requirePermission('mesh.terminal'), async (req, res) => {
    const id = req.params.id;
    if (await rejectIfMeshDeviceOutOfScope(req, res, id)) return;
    return proxyToGo(betterdeskApi.apiClient, req, res, 'POST', () => `/mesh/devices/${encodeURIComponent(id)}/exec`, req.body);
});

module.exports = router;
