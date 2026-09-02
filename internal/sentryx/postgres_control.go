package sentryx

import (
	"errors"
	"net/url"
	"strings"
)

func (p *PostgresStore) ListOrganizations() []Organization {
	rows, err := p.db.Query(`SELECT id, slug, name, date_created, status, allow_member_invite, allow_member_project_creation, require_2fa FROM sentryx_organizations ORDER BY slug`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]Organization, 0)
	for rows.Next() {
		var org Organization
		var status string
		if rows.Scan(&org.ID, &org.Slug, &org.Name, &org.DateCreated, &status, &org.AllowMemberInvite, &org.AllowMemberProjectCreation, &org.Require2FA) == nil {
			org.Status = map[string]string{"id": status, "name": status}
			result = append(result, org)
		}
	}
	return result
}

func (p *PostgresStore) GetOrganization(ref string) (Organization, bool) {
	var org Organization
	var status string
	err := p.db.QueryRow(`SELECT id, slug, name, date_created, status, allow_member_invite, allow_member_project_creation, require_2fa FROM sentryx_organizations WHERE id=$1 OR lower(slug)=lower($1) LIMIT 1`, ref).
		Scan(&org.ID, &org.Slug, &org.Name, &org.DateCreated, &status, &org.AllowMemberInvite, &org.AllowMemberProjectCreation, &org.Require2FA)
	if err != nil {
		return Organization{}, false
	}
	org.Status = map[string]string{"id": status, "name": status}
	return org, true
}

func (p *PostgresStore) orgID(ref string) (string, error) {
	var id string
	if err := p.db.QueryRow(`SELECT id FROM sentryx_organizations WHERE id=$1 OR lower(slug)=lower($1) LIMIT 1`, ref).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

func (p *PostgresStore) ListTeams(orgRef string) []Team {
	orgID, err := p.orgID(orgRef)
	if err != nil {
		return nil
	}
	rows, err := p.db.Query(`SELECT id, slug, name, date_created FROM sentryx_teams WHERE organization_id=$1 ORDER BY slug`, orgID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]Team, 0)
	for rows.Next() {
		var team Team
		team.OrganizationID = orgID
		if rows.Scan(&team.ID, &team.Slug, &team.Name, &team.DateCreated) == nil {
			team.HasAccess = true
			result = append(result, team)
		}
	}
	return result
}

func (p *PostgresStore) CreateTeam(orgRef, name, slug string) (Team, error) {
	orgID, err := p.orgID(orgRef)
	if err != nil {
		return Team{}, errors.New("organization not found")
	}
	if slug == "" {
		slug = slugify(name)
	}
	if slug == "" {
		return Team{}, errors.New("team slug is required")
	}
	if name == "" {
		name = slug
	}
	team := Team{ID: randomID(), OrganizationID: orgID, Slug: slug, Name: name, DateCreated: timeNowUTC(), HasAccess: true}
	_, err = p.db.Exec(`INSERT INTO sentryx_teams (id, organization_id, slug, name, date_created) VALUES ($1,$2,$3,$4,$5)`, team.ID, orgID, slug, name, team.DateCreated)
	if err != nil {
		return Team{}, err
	}
	return team, nil
}

func (p *PostgresStore) ListProjects(orgRef string) []ControlProject {
	orgID, err := p.orgID(orgRef)
	if err != nil {
		return nil
	}
	rows, err := p.db.Query(`SELECT id, slug, name, platform, date_created FROM sentryx_control_projects WHERE organization_id=$1 ORDER BY slug`, orgID)
	if err != nil {
		return nil
	}
	result := make([]ControlProject, 0)
	for rows.Next() {
		var project ControlProject
		project.OrganizationID = orgID
		if rows.Scan(&project.ID, &project.Slug, &project.Name, &project.Platform, &project.DateCreated) == nil {
			project.IsMember = true
			result = append(result, project)
		}
	}
	rows.Close()
	for i := range result {
		result[i].Teams = p.ListProjectTeams(orgID, result[i].ID)
		result[i].Keys = p.projectKeys(result[i].ID)
		if len(result[i].Teams) > 0 {
			result[i].Team = &result[i].Teams[0]
		}
	}
	return result
}

func (p *PostgresStore) resolveProject(orgRef, projectRef string) (ControlProject, error) {
	orgID, err := p.orgID(orgRef)
	if err != nil {
		return ControlProject{}, err
	}
	var project ControlProject
	err = p.db.QueryRow(`SELECT id, organization_id, slug, name, platform, date_created FROM sentryx_control_projects WHERE organization_id=$1 AND (id=$2 OR lower(slug)=lower($2)) LIMIT 1`, orgID, projectRef).Scan(&project.ID, &project.OrganizationID, &project.Slug, &project.Name, &project.Platform, &project.DateCreated)
	if err != nil {
		return ControlProject{}, err
	}
	project.IsMember = true
	project.Teams = p.ListProjectTeams(orgID, project.ID)
	project.Keys = p.projectKeys(project.ID)
	if len(project.Teams) > 0 {
		project.Team = &project.Teams[0]
	}
	return project, nil
}

func (p *PostgresStore) GetProject(orgRef, projectRef string) (ControlProject, bool) {
	project, err := p.resolveProject(orgRef, projectRef)
	return project, err == nil
}

func (p *PostgresStore) CreateProject(orgRef, name, slug, platform string) (ControlProject, error) {
	orgID, err := p.orgID(orgRef)
	if err != nil {
		return ControlProject{}, errors.New("organization not found")
	}
	if slug == "" {
		slug = slugify(name)
	}
	if slug == "" {
		return ControlProject{}, errors.New("project slug is required")
	}
	if name == "" {
		name = slug
	}
	project := ControlProject{ID: randomID(), OrganizationID: orgID, Slug: slug, Name: name, Platform: platform, DateCreated: timeNowUTC(), IsMember: true}
	tx, err := p.db.Begin()
	if err != nil {
		return ControlProject{}, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO sentryx_control_projects (id, organization_id, slug, name, platform, date_created) VALUES ($1,$2,$3,$4,$5,$6)`, project.ID, orgID, slug, name, platform, project.DateCreated)
	if err != nil {
		return ControlProject{}, err
	}
	key := ProjectKey{ID: randomID(), ProjectID: project.ID, Name: "Default", Public: "public-" + project.ID, Secret: ""}
	key.DSN = "http://" + key.Public + "@localhost/" + url.PathEscape(project.ID)
	if _, err = tx.Exec(`INSERT INTO sentryx_project_keys (id, project_id, name, public_key, secret_key) VALUES ($1,$2,$3,$4,$5)`, key.ID, project.ID, key.Name, key.Public, key.Secret); err != nil {
		return ControlProject{}, err
	}
	if err = tx.Commit(); err != nil {
		return ControlProject{}, err
	}
	project.Keys = []ProjectKey{key}
	return project, nil
}

func (p *PostgresStore) ListProjectTeams(orgRef, projectRef string) []Team {
	project, err := p.resolveProjectBase(orgRef, projectRef)
	if err != nil {
		return nil
	}
	rows, err := p.db.Query(`SELECT t.id, t.slug, t.name, t.date_created FROM sentryx_project_teams pt JOIN sentryx_teams t ON t.id=pt.team_id WHERE pt.project_id=$1 ORDER BY t.slug`, project.ID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]Team, 0)
	for rows.Next() {
		var team Team
		team.OrganizationID = project.OrganizationID
		if rows.Scan(&team.ID, &team.Slug, &team.Name, &team.DateCreated) == nil {
			team.HasAccess = true
			result = append(result, team)
		}
	}
	return result
}

func (p *PostgresStore) resolveProjectBase(orgRef, projectRef string) (ControlProject, error) {
	orgID, err := p.orgID(orgRef)
	if err != nil {
		return ControlProject{}, err
	}
	var project ControlProject
	err = p.db.QueryRow(`SELECT id, organization_id, slug, name, platform, date_created FROM sentryx_control_projects WHERE organization_id=$1 AND (id=$2 OR lower(slug)=lower($2)) LIMIT 1`, orgID, projectRef).Scan(&project.ID, &project.OrganizationID, &project.Slug, &project.Name, &project.Platform, &project.DateCreated)
	return project, err
}

func (p *PostgresStore) AddProjectTeam(orgRef, projectRef, teamRef string) (Team, error) {
	project, err := p.resolveProjectBase(orgRef, projectRef)
	if err != nil {
		return Team{}, errors.New("project not found")
	}
	var team Team
	team.OrganizationID = project.OrganizationID
	err = p.db.QueryRow(`SELECT id, slug, name, date_created FROM sentryx_teams WHERE organization_id=$1 AND (id=$2 OR lower(slug)=lower($2)) LIMIT 1`, project.OrganizationID, teamRef).Scan(&team.ID, &team.Slug, &team.Name, &team.DateCreated)
	if err != nil {
		return Team{}, errors.New("team not found")
	}
	_, err = p.db.Exec(`INSERT INTO sentryx_project_teams (project_id, team_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, project.ID, team.ID)
	team.HasAccess = true
	return team, err
}

func (p *PostgresStore) ListMembers(orgRef string) []OrgMember {
	orgID, err := p.orgID(orgRef)
	if err != nil {
		return nil
	}
	rows, err := p.db.Query(`SELECT m.id, m.role, m.date_created, m.expired, u.id, u.email, u.name, u.username, u.date_joined, u.avatar_url FROM sentryx_organization_members m JOIN sentryx_users u ON u.id=m.user_id WHERE m.organization_id=$1 ORDER BY m.date_created`, orgID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]OrgMember, 0)
	for rows.Next() {
		var member OrgMember
		member.OrganizationID = orgID
		if rows.Scan(&member.ID, &member.Role, &member.DateCreated, &member.Expired, &member.User.ID, &member.User.Email, &member.User.Name, &member.User.Username, &member.User.DateJoined, &member.User.AvatarURL) == nil {
			member.Email = member.User.Email
			member.Name = member.User.Name
			result = append(result, member)
		}
	}
	return result
}

func (p *PostgresStore) GetUser(userID string) (ControlUser, bool) {
	var user ControlUser
	err := p.db.QueryRow(`SELECT id, email, name, username, date_joined, avatar_url FROM sentryx_users WHERE id=$1`, userID).Scan(&user.ID, &user.Email, &user.Name, &user.Username, &user.DateJoined, &user.AvatarURL)
	return user, err == nil
}

func (p *PostgresStore) projectKeys(projectID string) []ProjectKey {
	rows, err := p.db.Query(`SELECT id, name, public_key, secret_key FROM sentryx_project_keys WHERE project_id=$1 ORDER BY id`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]ProjectKey, 0)
	for rows.Next() {
		var key ProjectKey
		key.ProjectID = projectID
		if rows.Scan(&key.ID, &key.Name, &key.Public, &key.Secret) == nil {
			key.DSN = "http://" + key.Public + "@localhost/" + url.PathEscape(projectID)
			result = append(result, key)
		}
	}
	return result
}

func (p *PostgresStore) ListProjectKeys(orgRef, projectRef string) []ProjectKey {
	project, ok := p.GetProject(orgRef, projectRef)
	if !ok {
		return nil
	}
	return p.projectKeys(project.ID)
}

func (p *PostgresStore) CreateProjectKey(orgRef, projectRef, name string) (ProjectKey, error) {
	project, ok := p.GetProject(orgRef, projectRef)
	if !ok {
		return ProjectKey{}, errors.New("project not found")
	}
	if strings.TrimSpace(name) == "" {
		name = "Key"
	}
	key := ProjectKey{ID: randomID(), ProjectID: project.ID, Name: name, Public: "public-" + randomID()[:16], Secret: randomID()}
	key.DSN = "http://" + url.QueryEscape(key.Public) + "@localhost/" + url.PathEscape(project.ID)
	_, err := p.db.Exec(`INSERT INTO sentryx_project_keys (id,project_id,name,public_key,secret_key) VALUES ($1,$2,$3,$4,$5)`, key.ID, key.ProjectID, key.Name, key.Public, key.Secret)
	return key, err
}

func (p *PostgresStore) RevokeProjectKey(orgRef, projectRef, keyID string) error {
	project, ok := p.GetProject(orgRef, projectRef)
	if !ok {
		return errors.New("project not found")
	}
	var count int
	if err := p.db.QueryRow(`SELECT count(*) FROM sentryx_project_keys WHERE project_id=$1`, project.ID).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return errors.New("cannot revoke the last project key")
	}
	result, err := p.db.Exec(`DELETE FROM sentryx_project_keys WHERE project_id=$1 AND id=$2`, project.ID, keyID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return errors.New("project key not found")
	}
	return nil
}

func (p *PostgresStore) ValidProjectKey(projectID, publicKey string) bool {
	var count int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM sentryx_project_keys WHERE project_id=$1`, projectID).Scan(&count); err != nil || count == 0 {
		return true
	}
	var exists bool
	_ = p.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sentryx_project_keys WHERE project_id=$1 AND public_key=$2)`, projectID, publicKey).Scan(&exists)
	return exists
}

func (p *PostgresStore) EnsureProject(projectID, publicKey string) error {
	if strings.TrimSpace(projectID) == "" {
		return errors.New("project id is required")
	}
	if strings.TrimSpace(publicKey) == "" {
		publicKey = "public-" + projectID
	}
	tx, err := p.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO sentryx_organizations (id, slug, name) VALUES ('1','default','Default') ON CONFLICT (id) DO NOTHING`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO sentryx_control_projects (id, organization_id, slug, name, platform) VALUES ($1,'1',$1,$1,'javascript') ON CONFLICT (id) DO NOTHING`, projectID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO sentryx_project_keys (id, project_id, name, public_key, secret_key)
		SELECT $1,$2,'Default',$3,''
		WHERE NOT EXISTS (SELECT 1 FROM sentryx_project_keys WHERE project_id=$2)
		ON CONFLICT (public_key) DO NOTHING`, randomID(), projectID, publicKey); err != nil {
		return err
	}
	return tx.Commit()
}

var _ ControlPlane = (*PostgresStore)(nil)
