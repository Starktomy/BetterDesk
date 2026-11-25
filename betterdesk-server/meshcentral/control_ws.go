package meshcentral

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/unitronix/betterdesk-server/auth"
)

// HandleControlWS uses the API principal and checks permissions for each action.
func (g *Gateway) HandleControlWS(w http.ResponseWriter, r *http.Request, userID string, authorize func(string, []string) map[string]bool) {
	if !g.allowIP(w, r) {
		return
	}
	if userID == "" || authorize == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, g.webSocketAcceptOptions())
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx := r.Context()

	// Push initial nodes list
	g.sendNodesList(ctx, conn, authorize)

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		action, _ := msg["action"].(string)
		switch action {
		case "ping":
			conn.Write(ctx, websocket.MessageText, []byte(`{"action":"pong"}`))
		case "authcookie":
			cookie, err := g.cookies.Encode(&RelayCookieData{RUserID: userID, ExpireMin: 240}, 240)
			if err == nil {
				resp, _ := json.Marshal(map[string]string{"action": "authcookie", "cookie": cookie})
				conn.Write(ctx, websocket.MessageText, resp)
			}
		case "nodes":
			g.sendNodesList(ctx, conn, authorize)
		case "runcommands":
			nodeid, _ := msg["nodeid"].(string)
			cmds, _ := msg["cmds"].(string)
			peerID := g.peerIDFromNode(nodeid)
			if peerID != "" && authorize(auth.PermMeshTerminal, []string{peerID})[peerID] {
				_ = g.SendRunCommand(peerID, userID, cmds, true)
			}
		case "devicepower":
			nodeid, _ := msg["nodeid"].(string)
			peerID := g.peerIDFromNode(nodeid)
			if peerID == "" || !authorize(auth.PermMeshPower, []string{peerID})[peerID] {
				continue
			}
			forced := false
			if f, ok := msg["forced"].(float64); ok && int(f) == 1 {
				forced = true
			}
			actionType := 0
			switch v := msg["actiontype"].(type) {
			case float64:
				actionType = int(v)
			case int:
				actionType = v
			}
			if actionType == 0 {
				if typ, ok := msg["type"].(string); ok {
					switch strings.ToLower(typ) {
					case "wake", "wakeup", "on":
						actionType = 6
					case "sleep":
						actionType = 2
					case "off", "poweroff":
						actionType = 3
					case "reset":
						actionType = 4
					}
				}
			}
			if actionType > 0 {
				_ = g.SendPowerAction(peerID, userID, actionType, forced)
			}
		default:
			log.Printf("[mesh] control action %s from %s", action, userID)
		}
	}
}

func (g *Gateway) sendNodesList(ctx context.Context, conn *websocket.Conn, authorize func(string, []string) map[string]bool) {
	var peerIDs []string
	g.agents.Range(func(key, value any) bool {
		if ac := value.(*AgentConn); ac.authenticated {
			peerIDs = append(peerIDs, key.(string))
		}
		return true
	})
	visible := authorize(auth.PermDeviceView, peerIDs)
	nodes := []map[string]interface{}{}
	for _, pid := range peerIDs {
		v, ok := g.agents.Load(pid)
		if !ok || !visible[pid] {
			continue
		}
		ac := v.(*AgentConn)
		nodes = append(nodes, map[string]interface{}{
			"_id":     "node//" + ac.meshNodeID,
			"name":    ac.hostname,
			"peer_id": pid,
			"conn":    1,
			"agent": map[string]interface{}{
				"ver":  g.assets.Version,
				"id":   ac.meshNodeID,
				"caps": 0,
			},
		})
	}
	resp := map[string]interface{}{
		"action": "nodes",
		"nodes":  nodes,
	}
	b, _ := json.Marshal(resp)
	conn.Write(ctx, websocket.MessageText, b)
}

func (g *Gateway) peerIDFromNode(nodeKey string) string {
	raw := strings.TrimPrefix(nodeKey, "node//")
	var found string
	g.agents.Range(func(key, value any) bool {
		pid, _ := key.(string)
		ac, _ := value.(*AgentConn)
		if ac != nil && ac.meshNodeID == raw {
			found = pid
			return false
		}
		return true
	})
	return found
}
