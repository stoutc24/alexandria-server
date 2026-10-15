package provision

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
)

// Calibre-Web (janeczku/calibre-web cps/admin.py, cps/web.py and
// cps/constants.py, read 2026-10-14) has no API for accounts, only its own
// web forms, so the admin's browser session is played: GET /login for the
// form's CSRF token, POST /login; GET /admin/user/new for a fresh token and
// the server's OWN defaults for a new user (its sidebar, its default
// language and locale), POST it back with the person's name, email and
// password.
//
//   - the roles are this server's, not the defaults': downloads and the
//     book viewer, nothing else (owner, 2026-10-14: "read only, plus
//     download for media"). Out of the box Calibre-Web's defaults give a
//     new user no roles at all, so no downloads;
//   - _handle_new_user needs name, email and password, reads
//     default_language unconditionally, adds up the show_<n> sidebar
//     boxes, and takes the roles from ALL_ROLES' box names;
//   - success is a redirect; a refusal re-renders the form with a flash;
//   - GET /ajax/listusers (admin) → {rows: [{id, name, email, role}]},
//     role bit 1 being admin.
//
// An existing account's password is NOT changed here: Calibre-Web's edit
// form recomputes the roles and sidebar from what is posted, so a
// password-only edit would strip them.
type calibreWebProvider struct{}

func (calibreWebProvider) Capability() Capability {
	return Capability{Type: "calibreweb", Create: true, Reset: false, Email: EmailRequired,
		Note: "Calibre-Web's edit form would reset an existing account's permissions, so its password is left alone."}
}

var (
	cwCSRF     = regexp.MustCompile(`(?is)<input[^>]*name="csrf_token"[^>]*>`)
	cwValue    = regexp.MustCompile(`(?is)value="([^"]*)"`)
	cwName     = regexp.MustCompile(`(?is)name="([^"]+)"`)
	cwCheckbox = regexp.MustCompile(`(?is)<input[^>]*type="checkbox"[^>]*>`)
	cwChecked  = regexp.MustCompile(`(?is)\schecked(\s|=|/?>)`)
	cwFlash    = regexp.MustCompile(`(?is)<div[^>]*class="[^"]*alert-danger[^"]*"[^>]*>(.*?)</div>`)
	cwTags     = regexp.MustCompile(`(?s)<[^>]*>`)
)

// cwRoles is what a new Calibre-Web account may do: download, and read in
// the book viewer.
var cwRoles = []string{"download_role", "viewer_role"}

func cwSelected(page, name string) string {
	re := regexp.MustCompile(`(?is)<select[^>]*name="` + regexp.QuoteMeta(name) + `"[^>]*>(.*?)</select>`)
	m := re.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	opt := regexp.MustCompile(`(?is)<option[^>]*\sselected[^>]*>`).FindString(m[1])
	if opt == "" {
		return ""
	}
	if v := cwValue.FindStringSubmatch(opt); v != nil {
		return html.UnescapeString(v[1])
	}
	return ""
}

func cwToken(page string) string {
	tag := cwCSRF.FindString(page)
	if v := cwValue.FindStringSubmatch(tag); v != nil {
		return html.UnescapeString(v[1])
	}
	return ""
}

func (calibreWebProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, _ bool) (Result, error) {
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{
		Timeout:   hc.Timeout,
		Transport: hc.Transport,
		Jar:       jar,
		// A redirect is the answer to read (success), not to follow.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	base := strings.TrimRight(strings.TrimSpace(admin.BaseURL), "/")
	get := func(path string) (int, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return 0, "", err
		}
		res, err := browser.Do(req)
		if err != nil {
			return 0, "", &UnreachableError{Err: err}
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		return res.StatusCode, string(body), nil
	}
	post := func(path string, form url.Values) (int, string, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(form.Encode()))
		if err != nil {
			return 0, "", "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Referer", base+path)
		res, err := browser.Do(req)
		if err != nil {
			return 0, "", "", &UnreachableError{Err: err}
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		return res.StatusCode, res.Header.Get("Location"), string(body), nil
	}

	// Sign in as the admin.
	status, page, err := get("/login")
	if err != nil {
		return Result{}, err
	}
	if status != http.StatusOK {
		return Result{}, &SignInError{Err: &ServiceError{Status: status, Message: "the sign-in page did not open"}}
	}
	status, location, _, err := post("/login", url.Values{
		"username": {admin.Username}, "password": {admin.Password},
		"remember_me": {"on"}, "csrf_token": {cwToken(page)},
	})
	if err != nil {
		return Result{}, err
	}
	if (status != http.StatusFound && status != http.StatusSeeOther) || strings.Contains(location, "/login") {
		return Result{}, &SignInError{Err: errors.New("Calibre-Web refused the sign-in")}
	}

	// Who is there already.
	type cwUser struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
		Role  int    `json:"role"`
	}
	list := func() ([]cwUser, error) {
		var out struct {
			Rows []cwUser `json:"rows"`
		}
		c := newClient(browser, base)
		if err := c.call(ctx, http.MethodGet, "/ajax/listusers?offset=0&limit=1000&sort=id&order=asc", nil, &out); err != nil {
			var se *ServiceError
			if errors.As(err, &se) && se.Status == http.StatusFound {
				return nil, &SignInError{Err: errors.New("the admin sign-in has no admin rights")}
			}
			return nil, err
		}
		return out.Rows, nil
	}
	users, err := list()
	if err != nil {
		return Result{}, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.Name, p.Username) {
			continue
		}
		if u.Role&1 == 1 {
			return Result{}, ErrAdminAccount
		}
		return Result{}, ErrResetUnsupported
	}
	if p.Email == "" {
		return Result{}, ErrEmailRequired
	}

	// The new-user form, with the server's own defaults.
	status, page, err = get("/admin/user/new")
	if err != nil {
		return Result{}, err
	}
	if status != http.StatusOK {
		return Result{}, &ServiceError{Status: status, Message: "the new-user form did not open"}
	}
	form := url.Values{
		"csrf_token":       {cwToken(page)},
		"name":             {p.Username},
		"email":            {p.Email},
		"password":         {p.Password},
		"kindle_mail":      {""},
		"default_language": {"all"},
	}
	if v := cwSelected(page, "default_language"); v != "" {
		form.Set("default_language", v)
	}
	if v := cwSelected(page, "locale"); v != "" {
		form.Set("locale", v)
	}
	// The server's own sidebar...
	for _, box := range cwCheckbox.FindAllString(page, -1) {
		name := cwName.FindStringSubmatch(box)
		if name == nil || !cwChecked.MatchString(box) {
			continue
		}
		if strings.HasPrefix(name[1], "show_") {
			form.Set(name[1], "on")
		}
	}
	// ...and read-and-download, whatever its defaults are.
	for _, role := range cwRoles {
		form.Set(role, "on")
	}
	status, _, body, err := post("/admin/user/new", form)
	if err != nil {
		return Result{}, err
	}
	if status != http.StatusFound && status != http.StatusSeeOther {
		msg := "Calibre-Web did not take the new account"
		if m := cwFlash.FindStringSubmatch(body); m != nil {
			if t := strings.TrimSpace(html.UnescapeString(cwTags.ReplaceAllString(m[1], ""))); t != "" {
				msg = t
			}
		}
		return Result{}, &ServiceError{Status: http.StatusBadRequest, Message: msg}
	}
	// A redirect can still carry a refusal: believe the list.
	users, err = list()
	if err != nil {
		return Result{}, err
	}
	for _, u := range users {
		if strings.EqualFold(u.Name, p.Username) {
			return Result{Outcome: Created}, nil
		}
	}
	return Result{}, &ServiceError{Status: http.StatusBadRequest, Message: "Calibre-Web did not take the new account"}
}
