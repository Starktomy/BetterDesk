package meshcentral

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MeshRecording describes a server-side mesh session capture file.
type MeshRecording struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	PeerID      string `json:"peer_id,omitempty"`
	SessionType string `json:"session_type,omitempty"`
	Size        int64  `json:"size"`
	Modified    string `json:"modified"`
	Transport   string `json:"transport"`
}

func meshRecordingsDir(dataDir string) string {
	return filepath.Join(dataDir, "mesh-recordings")
}

// ListMeshRecordings returns mesh capture files in dataDir/mesh-recordings.
func ListMeshRecordings(dataDir string) ([]MeshRecording, error) {
	dir := meshRecordingsDir(dataDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]MeshRecording, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".mcrec") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		rec := MeshRecording{
			ID:        e.Name(),
			Filename:  e.Name(),
			Size:      info.Size(),
			Modified:  info.ModTime().Format(time.RFC3339),
			Transport: "mesh",
		}
		rec.PeerID, rec.SessionType = meshRecordingIdentity(e.Name())
		out = append(out, rec)
	}
	return out, nil
}

func meshRecordingIdentity(id string) (peerID, sessionType string) {
	stem := strings.TrimSuffix(id, filepath.Ext(id))
	parts := strings.Split(stem, "_")
	if len(parts) < 4 {
		return "", ""
	}
	end := len(parts) - 3
	if _, err := time.Parse("20060102-150405", parts[end+1]); err != nil {
		return "", ""
	}
	return strings.Join(parts[:end], "_"), parts[end]
}

// MeshRecordingPeerID returns the device ID stored in the recording filename.
func MeshRecordingPeerID(id string) string {
	peerID, _ := meshRecordingIdentity(id)
	return peerID
}

// MeshRecordingPath resolves a recording file path by id (filename).
func MeshRecordingPath(dataDir, id string) (string, error) {
	if !strings.HasSuffix(strings.ToLower(id), ".mcrec") || strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return "", os.ErrInvalid
	}
	path := filepath.Join(meshRecordingsDir(dataDir), id)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", os.ErrInvalid
	}
	return path, nil
}

// SessionRecording is a unified list entry (mesh server-side; other transports client-local).
type SessionRecording struct {
	ID          string `json:"id"`
	Filename    string `json:"filename,omitempty"`
	PeerID      string `json:"peer_id,omitempty"`
	SessionType string `json:"session_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Modified    string `json:"modified,omitempty"`
	Transport   string `json:"transport"`
	Note        string `json:"note,omitempty"`
}

// ListSessionRecordings aggregates mesh server recordings for the unified panel list.
func ListSessionRecordings(dataDir string) ([]SessionRecording, error) {
	mesh, err := ListMeshRecordings(dataDir)
	if err != nil {
		return nil, err
	}
	out := make([]SessionRecording, 0, len(mesh))
	for _, m := range mesh {
		out = append(out, SessionRecording{
			ID:          m.ID,
			Filename:    m.Filename,
			PeerID:      m.PeerID,
			SessionType: m.SessionType,
			Size:        m.Size,
			Modified:    m.Modified,
			Transport:   "mesh",
		})
	}
	return out, nil
}
