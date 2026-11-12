// Package directory talks to the corporate directory (Active Directory in production,
// OpenLDAP in development).
package directory

import (
	"errors"
	"fmt"

	"github.com/go-ldap/ldap/v3"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

// Profile is what we read from the directory. Attribute names follow the dev
// OpenLDAP schema; for Active Directory set UserAttr=sAMAccountName and adapt
// the attribute list below (displayName, mail, department, physicalDeliveryOfficeName).
type Profile struct {
	Username    string
	DisplayName string
	Email       string
	Extension   string
	Department  string
	SiteCode    string
}

type Client struct {
	URL, BaseDN, UserAttr, BindDN, BindPassword string
	// UserFilter selects person entries for the nightly sync, e.g. (objectClass=inetOrgPerson).
	UserFilter string
}

var attrs = []string{"dn", "uid", "cn", "mail", "telephoneNumber", "departmentNumber", "physicalDeliveryOfficeName"}

func (c Client) profile(e *ldap.Entry) *Profile {
	return &Profile{
		Username:    e.GetAttributeValue(c.UserAttr),
		DisplayName: e.GetAttributeValue("cn"),
		Email:       e.GetAttributeValue("mail"),
		Extension:   e.GetAttributeValue("telephoneNumber"),
		Department:  e.GetAttributeValue("departmentNumber"),
		SiteCode:    e.GetAttributeValue("physicalDeliveryOfficeName"),
	}
}

func (c Client) serviceConn() (*ldap.Conn, error) {
	conn, err := ldap.DialURL(c.URL)
	if err != nil {
		return nil, fmt.Errorf("ldap dial: %w", err)
	}
	if err := conn.Bind(c.BindDN, c.BindPassword); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ldap service bind: %w", err)
	}
	return conn, nil
}

// Authenticate performs search-then-bind. Empty passwords are rejected up front:
// an LDAP bind with an empty password is an anonymous bind and would "succeed".
func (c Client) Authenticate(username, password string) (*Profile, error) {
	if username == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	conn, err := c.serviceConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	res, err := conn.Search(ldap.NewSearchRequest(
		c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 5, false,
		fmt.Sprintf("(%s=%s)", c.UserAttr, ldap.EscapeFilter(username)), attrs, nil))
	if err != nil {
		return nil, fmt.Errorf("ldap search: %w", err)
	}
	if len(res.Entries) != 1 {
		return nil, ErrInvalidCredentials
	}
	e := res.Entries[0]
	if err := conn.Bind(e.DN, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("ldap user bind: %w", err)
	}
	return c.profile(e), nil
}

// ListAll returns every person entry (used by the nightly directory sync).
func (c Client) ListAll() ([]*Profile, error) {
	conn, err := c.serviceConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	filter := c.UserFilter
	if filter == "" {
		filter = "(objectClass=inetOrgPerson)"
	}
	res, err := conn.SearchWithPaging(ldap.NewSearchRequest(
		c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 30, false, filter, attrs, nil), 500)
	if err != nil {
		return nil, fmt.Errorf("ldap list: %w", err)
	}
	out := make([]*Profile, 0, len(res.Entries))
	for _, e := range res.Entries {
		if p := c.profile(e); p.Username != "" {
			out = append(out, p)
		}
	}
	return out, nil
}
