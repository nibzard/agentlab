package daemon

import (
	"net/http"
	"strings"

	"github.com/agentlab/agentlab/internal/user"
)

// UserAPI provides HTTP endpoints for user and team management.
type UserAPI struct {
	registry *user.Registry
}

// NewUserAPI creates a new user API handler.
func NewUserAPI(registry *user.Registry) *UserAPI {
	return &UserAPI{registry: registry}
}

// Register registers user and team API endpoints on the given mux.
func (api *UserAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/users", api.handleUsers)
	mux.HandleFunc("/v1/users/", api.handleUserResource)
	mux.HandleFunc("/v1/teams", api.handleTeams)
	mux.HandleFunc("/v1/teams/", api.handleTeamResource)
}

func (api *UserAPI) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.listUsers(w, r)
	case http.MethodPost:
		api.addUser(w, r)
	default:
		writeMethodNotAllowed(w, []string{"GET", "POST"})
	}
}

func (api *UserAPI) handleUserResource(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/v1/users/")
	if suffix == "" {
		api.handleUsers(w, r)
		return
	}

	parts := strings.SplitN(suffix, "/", 3)
	name := parts[0]

	if len(parts) >= 2 && parts[1] == "keys" {
		api.handleUserKeys(w, r, name)
		return
	}

	switch r.Method {
	case http.MethodGet:
		api.showUser(w, r, name)
	case http.MethodDelete:
		api.removeUser(w, r, name)
	default:
		writeMethodNotAllowed(w, []string{"GET", "DELETE"})
	}
}

func (api *UserAPI) addUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Key  string `json:"key"`
		Role string `json:"role"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSONDecodeError(w, err)
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	role := user.RoleUser
	if strings.TrimSpace(req.Role) == "admin" {
		role = user.RoleAdmin
	}

	u, err := api.registry.AddUser(r.Context(), req.Name, req.Key, role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          u.ID,
		"name":        u.Name,
		"role":        string(u.Role),
		"fingerprint": u.Fingerprint,
		"created_at":  u.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func (api *UserAPI) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := api.registry.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var result []map[string]any
	for _, u := range users {
		result = append(result, map[string]any{
			"id":          u.ID,
			"name":        u.Name,
			"role":        string(u.Role),
			"fingerprint": u.Fingerprint,
			"created_at":  u.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	if result == nil {
		result = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": result})
}

func (api *UserAPI) showUser(w http.ResponseWriter, r *http.Request, name string) {
	u, err := api.registry.Store().GetUserByName(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          u.ID,
		"name":        u.Name,
		"role":        string(u.Role),
		"fingerprint": u.Fingerprint,
		"created_at":  u.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func (api *UserAPI) removeUser(w http.ResponseWriter, r *http.Request, name string) {
	err := api.registry.RemoveUser(r.Context(), name, name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "name": name})
}

func (api *UserAPI) handleUserKeys(w http.ResponseWriter, r *http.Request, userName string) {
	switch r.Method {
	case http.MethodPost:
		api.addUserKey(w, r, userName)
	case http.MethodDelete:
		api.removeUserKey(w, r, userName)
	default:
		writeMethodNotAllowed(w, []string{"POST", "DELETE"})
	}
}

func (api *UserAPI) addUserKey(w http.ResponseWriter, r *http.Request, userName string) {
	var req struct {
		Key string `json:"key"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSONDecodeError(w, err)
		return
	}
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	u, err := api.registry.Store().GetUserByName(r.Context(), userName)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	err = api.registry.Store().AddSSHKey(r.Context(), u.ID, req.Key, req.Key, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	_ = api.registry.RecordAction(r.Context(), u.ID, "user.key.add", "user:"+userName, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "added"})
}

func (api *UserAPI) removeUserKey(w http.ResponseWriter, r *http.Request, userName string) {
	fingerprint := r.URL.Query().Get("fingerprint")
	if fingerprint == "" {
		writeError(w, http.StatusBadRequest, "fingerprint is required")
		return
	}

	u, err := api.registry.Store().GetUserByName(r.Context(), userName)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	err = api.registry.Store().RemoveSSHKey(r.Context(), u.ID, fingerprint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	_ = api.registry.RecordAction(r.Context(), u.ID, "user.key.remove", "user:"+userName, "fingerprint="+fingerprint)
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// --- Team handlers ---

func (api *UserAPI) handleTeams(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.listTeams(w, r)
	case http.MethodPost:
		api.addTeam(w, r)
	default:
		writeMethodNotAllowed(w, []string{"GET", "POST"})
	}
}

func (api *UserAPI) handleTeamResource(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/v1/teams/")
	if suffix == "" {
		api.handleTeams(w, r)
		return
	}

	parts := strings.SplitN(suffix, "/", 3)
	teamName := parts[0]

	if len(parts) >= 2 && parts[1] == "members" {
		if len(parts) == 3 && parts[2] != "" {
			if r.Method == http.MethodDelete {
				api.removeTeamMember(w, r, teamName, parts[2])
				return
			}
			writeMethodNotAllowed(w, []string{"DELETE"})
			return
		}
		api.handleTeamMembers(w, r, teamName)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		api.removeTeam(w, r, teamName)
	default:
		writeMethodNotAllowed(w, []string{"DELETE"})
	}
}

func (api *UserAPI) addTeam(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSONDecodeError(w, err)
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	t, err := api.registry.CreateTeam(r.Context(), req.Name, req.Description, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          t.ID,
		"name":        t.Name,
		"description": t.Description,
		"created_at":  t.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func (api *UserAPI) listTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := api.registry.ListTeams(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var result []map[string]any
	for _, t := range teams {
		result = append(result, map[string]any{
			"id":          t.ID,
			"name":        t.Name,
			"description": t.Description,
			"created_at":  t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	if result == nil {
		result = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": result})
}

func (api *UserAPI) removeTeam(w http.ResponseWriter, r *http.Request, name string) {
	err := api.registry.RemoveTeam(r.Context(), name, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "name": name})
}

func (api *UserAPI) handleTeamMembers(w http.ResponseWriter, r *http.Request, teamName string) {
	switch r.Method {
	case http.MethodGet:
		api.listTeamMembers(w, r, teamName)
	case http.MethodPost:
		api.addTeamMember(w, r, teamName)
	default:
		writeMethodNotAllowed(w, []string{"GET", "POST"})
	}
}

func (api *UserAPI) listTeamMembers(w http.ResponseWriter, r *http.Request, teamName string) {
	members, err := api.registry.ListTeamMembers(r.Context(), teamName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var result []map[string]any
	for _, m := range members {
		result = append(result, map[string]any{
			"user_id":   m.UserID,
			"team_id":   m.TeamID,
			"role":      string(m.Role),
			"joined_at": m.JoinedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	if result == nil {
		result = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": result})
}

func (api *UserAPI) addTeamMember(w http.ResponseWriter, r *http.Request, teamName string) {
	var req struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSONDecodeError(w, err)
		return
	}
	if req.UserID == "" {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	role := user.RoleUser
	if strings.TrimSpace(req.Role) == "admin" {
		role = user.RoleAdmin
	}

	err := api.registry.AddTeamMember(r.Context(), teamName, req.UserID, role, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "added", "team": teamName, "user": req.UserID})
}

func (api *UserAPI) removeTeamMember(w http.ResponseWriter, r *http.Request, teamName, userName string) {
	err := api.registry.RemoveTeamMember(r.Context(), teamName, userName, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "team": teamName, "user": userName})
}

// Registry returns the underlying user registry.
func (api *UserAPI) Registry() *user.Registry {
	return api.registry
}
