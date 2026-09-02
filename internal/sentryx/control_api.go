package sentryx

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// isControlAPI separates Sentry's management endpoints from the existing
// project ingest, release, and artifact endpoints.
func (a *App) isControlAPI(parts []string) bool {
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "0" {
		return false
	}
	switch parts[2] {
	case "organizations", "users":
		return true
	case "projects":
		// /api/0/projects/{id}/releases is the legacy release API.
		if len(parts) < 5 || parts[4] == "releases" {
			return false
		}
		// Product analytics and alert rules are event-store APIs, not control-plane
		// endpoints, even though they share the project URL prefix.
		if len(parts) >= 6 && (parts[5] == "stats" || parts[5] == "alert-rules") {
			return false
		}
		return true
	default:
		return false
	}
}

func (a *App) handleControlAPI(w http.ResponseWriter, r *http.Request, parts []string) {
	if !a.validControlToken(r) {
		http.Error(w, "management authentication required", http.StatusUnauthorized)
		return
	}
	if a.Control == nil {
		http.Error(w, "control plane unavailable", http.StatusNotImplemented)
		return
	}
	switch parts[2] {
	case "users":
		a.handleUserAPI(w, r, parts)
	case "organizations":
		a.handleOrganizationAPI(w, r, parts)
	case "projects":
		a.handleProjectAPI(w, r, parts)
	default:
		http.NotFound(w, r)
	}
}

func (a *App) handleUserAPI(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method != http.MethodGet || len(parts) != 4 || parts[3] != "me" {
		http.NotFound(w, r)
		return
	}
	userID := a.CurrentUserID
	if userID == "" {
		userID = "1"
	}
	user, ok := a.Control.GetUser(userID)
	if !ok {
		user = ControlUser{ID: userID, Username: userID}
	}
	writeJSON(w, http.StatusOK, user)
}

func (a *App) handleOrganizationAPI(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 3 && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, a.Control.ListOrganizations())
		return
	}
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	orgRef := parts[3]
	if len(parts) == 4 && r.Method == http.MethodGet {
		org, ok := a.Control.GetOrganization(orgRef)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, org)
		return
	}
	if len(parts) == 5 && parts[4] == "teams" {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, a.Control.ListTeams(orgRef))
			return
		}
		if r.Method == http.MethodPost {
			var request struct {
				Name string `json:"name"`
				Slug string `json:"slug"`
			}
			if err := decodeJSONBody(r, &request); err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
			team, err := a.Control.CreateTeam(orgRef, strings.TrimSpace(request.Name), strings.TrimSpace(request.Slug))
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			writeJSON(w, http.StatusCreated, team)
			return
		}
	}
	if len(parts) == 5 && parts[4] == "projects" {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, a.Control.ListProjects(orgRef))
			return
		}
		if r.Method == http.MethodPost {
			var request struct {
				Name     string `json:"name"`
				Slug     string `json:"slug"`
				Platform string `json:"platform"`
			}
			if err := decodeJSONBody(r, &request); err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
			project, err := a.Control.CreateProject(orgRef, strings.TrimSpace(request.Name), strings.TrimSpace(request.Slug), strings.TrimSpace(request.Platform))
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			writeJSON(w, http.StatusCreated, project)
			return
		}
	}
	if len(parts) == 5 && parts[4] == "members" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, a.Control.ListMembers(orgRef))
		return
	}
	http.NotFound(w, r)
}

func (a *App) handleProjectAPI(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < 5 {
		http.NotFound(w, r)
		return
	}
	orgRef, projectRef := parts[3], parts[4]
	if len(parts) == 5 && r.Method == http.MethodGet {
		project, ok := a.Control.GetProject(orgRef, projectRef)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, project)
		return
	}
	if len(parts) == 6 && parts[5] == "teams" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, a.Control.ListProjectTeams(orgRef, projectRef))
		return
	}
	if len(parts) == 6 && parts[5] == "keys" {
		keys, ok := a.Control.(ProjectKeyStore)
		if !ok {
			http.Error(w, "project keys unavailable", http.StatusNotImplemented)
			return
		}
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, keys.ListProjectKeys(orgRef, projectRef))
			return
		}
		if r.Method == http.MethodPost {
			var request struct {
				Name string `json:"name"`
			}
			if err := decodeJSONBody(r, &request); err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
			key, err := keys.CreateProjectKey(orgRef, projectRef, request.Name)
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			writeJSON(w, http.StatusCreated, key)
			return
		}
	}
	if len(parts) == 7 && parts[5] == "keys" && r.Method == http.MethodDelete {
		keys, ok := a.Control.(ProjectKeyStore)
		if !ok {
			http.Error(w, "project keys unavailable", http.StatusNotImplemented)
			return
		}
		if err := keys.RevokeProjectKey(orgRef, projectRef, parts[6]); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 7 && parts[5] == "teams" && r.Method == http.MethodPost {
		team, err := a.Control.AddProjectTeam(orgRef, projectRef, parts[6])
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusCreated, team)
		return
	}
	http.NotFound(w, r)
}

func decodeJSONBody(r *http.Request, destination any) error {
	if r.Body == nil {
		return io.EOF
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return decoder.Decode(destination)
}

func (a *App) validControlToken(r *http.Request) bool {
	token := strings.TrimSpace(r.Header.Get("X-SentryX-Management-Token"))
	if token == "" {
		value := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(value), "bearer ") {
			token = strings.TrimSpace(value[len("bearer "):])
		}
	}
	if len(a.APITokens) > 0 {
		if _, ok := a.APITokens[token]; ok {
			return true
		}
	}
	if len(a.APITokenHashes) > 0 {
		_, ok := a.APITokenHashes[HashAPIToken(token)]
		return ok
	}
	if a.ArtifactToken != "" {
		return len(token) == len(a.ArtifactToken) && subtle.ConstantTimeCompare([]byte(token), []byte(a.ArtifactToken)) == 1
	}
	// Keep local development and existing installations backwards compatible;
	// production deployments should set SENTRYX_API_TOKENS or artifact token.
	return true
}
