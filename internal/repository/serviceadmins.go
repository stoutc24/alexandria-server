package repository

// Server Admin sign-ins: the household admin's own admin logins to the
// servers it runs, kept here so that every phone signed in to Manage server
// can sign its Server Admin in to all of them (owner, 2026-10-13: "I host
// like 12 services, but only need to be signed into a few for use. I'd like
// to still be able to manage all the services in server admin"; "is there a
// way to have the companion server serve that to the app?").
//
// What is kept is what a phone needs to sign in by itself: the address, the
// username and the password, the password sealed with the secrets key. Each
// phone signs in on its own and so holds its own session — a token handed
// from phone to phone would break wherever a server rotates its refresh
// tokens (Grimmory revokes the one it is given).
//
// Plex is not kept, on purpose: its admin sign-in is the owner's whole Plex
// account token, which never leaves the phone that approved it.
//
// Reading a password back is for an admin phone that brings a fresh code
// from the authenticator, boxed to a key the phone made for that request
// (handlers.ServiceAdminsHandler.Open). The web page saves, lists and
// removes them, and never shows one.

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/vault"
)

// ServiceAdminTypes are the services Server Admin signs in to with a
// username and a password, by the app's names for them.
var ServiceAdminTypes = map[string]string{
	"abs":         "Audiobookshelf",
	"jellyfin":    "Jellyfin",
	"kavita":      "Kavita",
	"komga":       "Komga",
	"navidrome":   "Navidrome",
	"booklore":    "Grimmory",
	"storyteller": "Storyteller",
	"stump":       "Stump",
	"calibreweb":  "Calibre-Web",
}

// ErrBadServiceAdmin: an unknown service, an address that is not http(s),
// or a missing username or password.
var ErrBadServiceAdmin = errors.New("a sign-in needs a known service, an http(s) address, a username and a password")

// ErrServiceAdminNotFound: no kept sign-in has that id.
var ErrServiceAdminNotFound = errors.New("no such sign-in")

const (
	maxServiceAdminUsername = 200  // bytes, cut between characters
	maxServiceAdminPassword = 1024 // bytes; refused, never cut
)

// ServiceAdmin is one kept sign-in, without its password.
type ServiceAdmin struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	BaseURL   string `json:"baseUrl"`
	Username  string `json:"username"`
	UpdatedAt int64  `json:"updatedAt"`
}

// OpenedServiceAdmin is one with its password, for a phone. Unreadable: it
// was sealed under a secrets key this server no longer has, so the phone
// keeps whatever sign-in it already holds.
type OpenedServiceAdmin struct {
	ServiceAdmin
	Password   string `json:"password,omitempty"`
	Unreadable bool   `json:"unreadable,omitempty"`
}

// ServiceAdminInput is what the page or a phone saves.
type ServiceAdminInput struct {
	Type     string `json:"type"`
	BaseURL  string `json:"baseUrl"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type ServiceAdminRepository struct {
	vault *vault.Vault
}

func NewServiceAdminRepository(v *vault.Vault) *ServiceAdminRepository {
	return &ServiceAdminRepository{vault: v}
}

// Available is whether sign-ins can be kept at all: they need the secrets
// key, as account services do.
func (r *ServiceAdminRepository) Available() bool { return r.vault != nil }

// The sealed password is bound to its service: there is one row per
// service, and a password copied onto another service's row will not open.
func serviceAdminAD(serviceType string) string { return "service-admin|" + serviceType }

// List is every kept sign-in, without passwords, by service.
func (r *ServiceAdminRepository) List() ([]ServiceAdmin, error) {
	rows, err := database.DB.Query(`
		SELECT id, service_type, base_url, username, updated_at
		FROM service_admin_logins ORDER BY service_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServiceAdmin{}
	for rows.Next() {
		var a ServiceAdmin
		if err := rows.Scan(&a.ID, &a.Type, &a.BaseURL, &a.Username, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.Name = ServiceAdminTypes[a.Type]
		out = append(out, a)
	}
	return out, rows.Err()
}

// Save keeps a sign-in, one per service: saving a service again replaces
// it, as a phone's Server Admin holds one of each. The password is sealed
// before it is written and is never read back here.
func (r *ServiceAdminRepository) Save(in ServiceAdminInput) (*ServiceAdmin, error) {
	if r.vault == nil {
		return nil, ErrVaultOff
	}
	name, ok := ServiceAdminTypes[in.Type]
	username := clip(strings.TrimSpace(in.Username), maxServiceAdminUsername)
	if !ok || username == "" || in.Password == "" ||
		len(in.Password) > maxServiceAdminPassword {
		return nil, ErrBadServiceAdmin
	}
	base, err := NormalizeServiceURL(in.BaseURL)
	if err != nil {
		return nil, ErrBadServiceAdmin
	}
	sealed, err := r.vault.Seal([]byte(in.Password), serviceAdminAD(in.Type))
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	// A replaced row keeps its id: ON CONFLICT leaves it as it was.
	if _, err := database.DB.Exec(`
		INSERT INTO service_admin_logins
			(id, service_type, base_url, username, secret, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(service_type) DO UPDATE SET
			base_url = excluded.base_url,
			username = excluded.username,
			secret = excluded.secret,
			updated_at = excluded.updated_at`,
		uuid.New().String(), in.Type, base, username, sealed, now, now); err != nil {
		return nil, err
	}
	var id string
	if err := database.DB.QueryRow(
		`SELECT id FROM service_admin_logins WHERE service_type = ?`, in.Type).Scan(&id); err != nil {
		return nil, err
	}
	return &ServiceAdmin{ID: id, Type: in.Type, Name: name, BaseURL: base,
		Username: username, UpdatedAt: now}, nil
}

// Delete removes one kept sign-in.
func (r *ServiceAdminRepository) Delete(id string) error {
	res, err := database.DB.Exec(`DELETE FROM service_admin_logins WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrServiceAdminNotFound
	}
	return nil
}

// OpenAll is every kept sign-in with its password, for the one route that
// hands them to a phone. A password that will not open is marked
// Unreadable rather than failing the rest.
func (r *ServiceAdminRepository) OpenAll() ([]OpenedServiceAdmin, error) {
	if r.vault == nil {
		return nil, ErrVaultOff
	}
	rows, err := database.DB.Query(`
		SELECT id, service_type, base_url, username, updated_at, secret
		FROM service_admin_logins ORDER BY service_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpenedServiceAdmin{}
	for rows.Next() {
		var a OpenedServiceAdmin
		var sealed sql.NullString
		if err := rows.Scan(&a.ID, &a.Type, &a.BaseURL, &a.Username, &a.UpdatedAt, &sealed); err != nil {
			return nil, err
		}
		a.Name = ServiceAdminTypes[a.Type]
		if plain, err := r.vault.Open(sealed.String, serviceAdminAD(a.Type)); err == nil {
			a.Password = string(plain)
		} else {
			a.Unreadable = true
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
