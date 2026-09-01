package sentryx

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// ControlUser, Organization, Team, and ControlProject model the Sentry
// control plane. The ingest wire format remains the official Sentry Envelope;
// these types only back the compatible /api/0 management endpoints.
type ControlUser struct {
	ID         string    `json:"id"`
	Email      string    `json:"email,omitempty"`
	Name       string    `json:"name,omitempty"`
	Username   string    `json:"username,omitempty"`
	DateJoined time.Time `json:"dateJoined,omitempty"`
	AvatarURL  string    `json:"avatarUrl,omitempty"`
}

type Organization struct {
	ID                         string            `json:"id"`
	Slug                       string            `json:"slug"`
	Name                       string            `json:"name"`
	DateCreated                time.Time         `json:"dateCreated"`
	Status                     map[string]string `json:"status,omitempty"`
	AllowMemberInvite          bool              `json:"allowMemberInvite"`
	AllowMemberProjectCreation bool              `json:"allowMemberProjectCreation"`
	Require2FA                 bool              `json:"require2FA"`
}

type Team struct {
	ID             string          `json:"id"`
	OrganizationID string          `json:"-"`
	Slug           string          `json:"slug"`
	Name           string          `json:"name"`
	DateCreated    time.Time       `json:"dateCreated"`
	IsMember       bool            `json:"isMember"`
	TeamRole       string          `json:"teamRole,omitempty"`
	MemberCount    int             `json:"memberCount"`
	HasAccess      bool            `json:"hasAccess"`
	IsPending      bool            `json:"isPending"`
	Flags          map[string]bool `json:"flags,omitempty"`
	Access         []string        `json:"access,omitempty"`
}

type OrgMember struct {
	ID             string      `json:"id"`
	OrganizationID string      `json:"-"`
	User           ControlUser `json:"user"`
	Email          string      `json:"email,omitempty"`
	Name           string      `json:"name,omitempty"`
	Role           string      `json:"role"`
	DateCreated    time.Time   `json:"dateCreated"`
	Expired        bool        `json:"expired"`
	Teams          []Team      `json:"teams,omitempty"`
}

type ProjectKey struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Public    string `json:"public"`
	Secret    string `json:"secret,omitempty"`
	DSN       string `json:"dsn"`
	ProjectID string `json:"-"`
}

type ControlProject struct {
	ID             string       `json:"id"`
	OrganizationID string       `json:"-"`
	Slug           string       `json:"slug"`
	Name           string       `json:"name"`
	Platform       string       `json:"platform,omitempty"`
	DateCreated    time.Time    `json:"dateCreated"`
	Team           *Team        `json:"team,omitempty"`
	Teams          []Team       `json:"teams,omitempty"`
	Keys           []ProjectKey `json:"keys,omitempty"`
	IsBookmarked   bool         `json:"isBookmarked"`
	IsMember       bool         `json:"isMember"`
	Features       []string     `json:"features,omitempty"`
}

type ControlPlane interface {
	ListOrganizations() []Organization
	GetOrganization(ref string) (Organization, bool)
	ListTeams(orgRef string) []Team
	CreateTeam(orgRef, name, slug string) (Team, error)
	ListProjects(orgRef string) []ControlProject
	GetProject(orgRef, projectRef string) (ControlProject, bool)
	CreateProject(orgRef, name, slug, platform string) (ControlProject, error)
	ListProjectTeams(orgRef, projectRef string) []Team
	AddProjectTeam(orgRef, projectRef, teamRef string) (Team, error)
	ListMembers(orgRef string) []OrgMember
	GetUser(userID string) (ControlUser, bool)
	ValidProjectKey(projectID, publicKey string) bool
	EnsureProject(projectID string)
}

type memoryControlPlane struct {
	mu            sync.RWMutex
	organizations map[string]Organization
	orgBySlug     map[string]string
	teams         map[string]Team
	projects      map[string]ControlProject
	projectBySlug map[string]string
	projectTeams  map[string]map[string]struct{}
	members       map[string][]OrgMember
	users         map[string]ControlUser
	keys          map[string][]ProjectKey
	next          uint64
}

func NewMemoryControlPlane() ControlPlane {
	now := time.Now().UTC()
	plane := &memoryControlPlane{
		organizations: make(map[string]Organization), orgBySlug: make(map[string]string),
		teams: make(map[string]Team), projects: make(map[string]ControlProject),
		projectBySlug: make(map[string]string), projectTeams: make(map[string]map[string]struct{}),
		members: make(map[string][]OrgMember), users: make(map[string]ControlUser), keys: make(map[string][]ProjectKey), next: 1000,
	}
	plane.organizations["1"] = Organization{ID: "1", Slug: "default", Name: "Default", DateCreated: now, Status: map[string]string{"id": "active", "name": "active"}, AllowMemberInvite: true, AllowMemberProjectCreation: true}
	plane.orgBySlug["default"] = "1"
	return plane
}

func (m *memoryControlPlane) nextID() string { m.next++; return fmt.Sprintf("%d", m.next) }

func (m *memoryControlPlane) resolveOrg(ref string) (Organization, bool) {
	if org, ok := m.organizations[ref]; ok {
		return org, true
	}
	id, ok := m.orgBySlug[strings.ToLower(ref)]
	if !ok {
		return Organization{}, false
	}
	org, ok := m.organizations[id]
	return org, ok
}

func (m *memoryControlPlane) ListOrganizations() []Organization {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Organization, 0, len(m.organizations))
	for _, org := range m.organizations {
		result = append(result, org)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Slug < result[j].Slug })
	return result
}

func (m *memoryControlPlane) GetOrganization(ref string) (Organization, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.resolveOrg(ref)
}

func (m *memoryControlPlane) ListTeams(orgRef string) []Team {
	m.mu.RLock()
	defer m.mu.RUnlock()
	org, ok := m.resolveOrg(orgRef)
	if !ok {
		return nil
	}
	result := make([]Team, 0)
	for _, team := range m.teams {
		if team.OrganizationID == org.ID {
			result = append(result, team)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Slug < result[j].Slug })
	return result
}

func (m *memoryControlPlane) CreateTeam(orgRef, name, slug string) (Team, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	org, ok := m.resolveOrg(orgRef)
	if !ok {
		return Team{}, errors.New("organization not found")
	}
	if slug == "" {
		slug = slugify(name)
	}
	if name == "" {
		name = slug
	}
	for _, team := range m.teams {
		if team.OrganizationID == org.ID && team.Slug == slug {
			return Team{}, errors.New("team already exists")
		}
	}
	team := Team{ID: m.nextID(), OrganizationID: org.ID, Slug: slug, Name: name, DateCreated: time.Now().UTC(), HasAccess: true, Flags: map[string]bool{"idp:provisioned": false}}
	m.teams[team.ID] = team
	return team, nil
}

func (m *memoryControlPlane) ListProjects(orgRef string) []ControlProject {
	m.mu.RLock()
	defer m.mu.RUnlock()
	org, ok := m.resolveOrg(orgRef)
	if !ok {
		return nil
	}
	result := make([]ControlProject, 0)
	for _, project := range m.projects {
		if project.OrganizationID == org.ID {
			result = append(result, m.withProjectRelations(project))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Slug < result[j].Slug })
	return result
}

func (m *memoryControlPlane) GetProject(orgRef, projectRef string) (ControlProject, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	org, ok := m.resolveOrg(orgRef)
	if !ok {
		return ControlProject{}, false
	}
	project, ok := m.projects[projectRef]
	if !ok {
		if id := m.projectBySlug[org.ID+":"+strings.ToLower(projectRef)]; id != "" {
			project, ok = m.projects[id]
		}
	}
	if !ok || project.OrganizationID != org.ID {
		return ControlProject{}, false
	}
	return m.withProjectRelations(project), true
}

func (m *memoryControlPlane) CreateProject(orgRef, name, slug, platform string) (ControlProject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	org, ok := m.resolveOrg(orgRef)
	if !ok {
		return ControlProject{}, errors.New("organization not found")
	}
	if slug == "" {
		slug = slugify(name)
	}
	if name == "" {
		name = slug
	}
	if m.projectBySlug[org.ID+":"+strings.ToLower(slug)] != "" {
		return ControlProject{}, errors.New("project already exists")
	}
	project := ControlProject{ID: m.nextID(), OrganizationID: org.ID, Slug: slug, Name: name, Platform: platform, DateCreated: time.Now().UTC(), IsMember: true, Features: []string{"event-attachments", "releases"}}
	m.projects[project.ID] = project
	m.projectBySlug[org.ID+":"+strings.ToLower(slug)] = project.ID
	key := ProjectKey{ID: m.nextID(), ProjectID: project.ID, Name: "Default", Public: "public-" + project.ID, Secret: "secret-" + project.ID}
	key.DSN = "http://" + key.Public + "@localhost/" + project.ID
	m.keys[project.ID] = []ProjectKey{key}
	return m.withProjectRelations(project), nil
}

func (m *memoryControlPlane) ListProjectTeams(orgRef, projectRef string) []Team {
	project, ok := m.GetProject(orgRef, projectRef)
	if !ok {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []Team
	for id := range m.projectTeams[project.ID] {
		if team, ok := m.teams[id]; ok {
			result = append(result, team)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Slug < result[j].Slug })
	return result
}

func (m *memoryControlPlane) AddProjectTeam(orgRef, projectRef, teamRef string) (Team, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	org, ok := m.resolveOrg(orgRef)
	if !ok {
		return Team{}, errors.New("organization not found")
	}
	project, ok := m.projects[projectRef]
	if !ok {
		if id := m.projectBySlug[org.ID+":"+strings.ToLower(projectRef)]; id != "" {
			project, ok = m.projects[id]
		}
	}
	team, teamOK := m.teams[teamRef]
	if !teamOK {
		for _, candidate := range m.teams {
			if candidate.OrganizationID == org.ID && candidate.Slug == teamRef {
				team, teamOK = candidate, true
				break
			}
		}
	}
	if !ok || project.OrganizationID != org.ID {
		return Team{}, errors.New("project not found")
	}
	if !teamOK || team.OrganizationID != org.ID {
		return Team{}, errors.New("team not found")
	}
	if m.projectTeams[project.ID] == nil {
		m.projectTeams[project.ID] = make(map[string]struct{})
	}
	m.projectTeams[project.ID][team.ID] = struct{}{}
	return team, nil
}

func (m *memoryControlPlane) ListMembers(orgRef string) []OrgMember {
	m.mu.RLock()
	defer m.mu.RUnlock()
	org, ok := m.resolveOrg(orgRef)
	if !ok {
		return nil
	}
	return append([]OrgMember(nil), m.members[org.ID]...)
}
func (m *memoryControlPlane) GetUser(userID string) (ControlUser, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	user, ok := m.users[userID]
	return user, ok
}

func (m *memoryControlPlane) ValidProjectKey(projectID, publicKey string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys, ok := m.keys[projectID]
	if !ok {
		return true
	}
	for _, key := range keys {
		if key.Public == publicKey {
			return true
		}
	}
	return false
}

func (m *memoryControlPlane) EnsureProject(projectID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[projectID]; ok {
		return
	}
	project := ControlProject{ID: projectID, OrganizationID: "1", Slug: projectID, Name: projectID, Platform: "javascript", DateCreated: time.Now().UTC(), IsMember: true}
	m.projects[projectID] = project
	m.projectBySlug["1:"+strings.ToLower(project.Slug)] = projectID
	key := ProjectKey{ID: m.nextID(), ProjectID: projectID, Name: "Default", Public: "public"}
	key.DSN = "http://public@localhost/" + url.PathEscape(projectID)
	m.keys[projectID] = []ProjectKey{key}
}

func (m *memoryControlPlane) withProjectRelations(project ControlProject) ControlProject {
	project.Teams = nil
	for id := range m.projectTeams[project.ID] {
		if team, ok := m.teams[id]; ok {
			project.Teams = append(project.Teams, team)
		}
	}
	if len(project.Teams) > 0 {
		project.Team = &project.Teams[0]
	}
	project.Keys = append([]ProjectKey(nil), m.keys[project.ID]...)
	return project
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
