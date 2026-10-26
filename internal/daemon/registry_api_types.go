package daemon

// Named wire types keep registry request/response schemas tied to the handlers.
type V1UserCreateRequest struct {
	Name string `json:"name"`
	Key  string `json:"key"`
	Role string `json:"role"`
}
type V1UserKeyCreateRequest struct {
	Key string `json:"key"`
}
type V1TeamCreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
type V1TeamMemberCreateRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}
type V1UserResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"created_at"`
}
type V1UsersResponse struct {
	Users []V1UserResponse `json:"users"`
}
type V1TeamResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}
type V1TeamsResponse struct {
	Teams []V1TeamResponse `json:"teams"`
}
type V1TeamMemberResponse struct {
	UserID   string `json:"user_id"`
	TeamID   string `json:"team_id"`
	Role     string `json:"role"`
	JoinedAt string `json:"joined_at"`
}
type V1TeamMembersResponse struct {
	Members []V1TeamMemberResponse `json:"members"`
}
type V1RegistryMutationResponse struct {
	Status      string `json:"status"`
	Name        string `json:"name,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Team        string `json:"team,omitempty"`
	User        string `json:"user,omitempty"`
}
