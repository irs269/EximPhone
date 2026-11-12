package main

import (
	"errors"
	"fmt"

	"github.com/go-ldap/ldap/v3"
)

var errInvalidCredentials = errors.New("invalid credentials")

// Profile is what we read from the directory. Attribute names follow the dev
// OpenLDAP schema; for Active Directory set LDAP_USER_ATTR=sAMAccountName and
// adapt the attribute list below (displayName, mail, department, physicalDeliveryOfficeName).
type Profile struct {
	Username    string
	DisplayName string
	Email       string
	Extension   string
	Department  string
	SiteCode    string
}

type ldapClient struct {
	url, baseDN, userAttr, bindDN, bindPassword string
}

// Authenticate performs search-then-bind. Empty passwords are rejected up front:
// an LDAP bind with an empty password is an anonymous bind and would "succeed".
func (c ldapClient) Authenticate(username, password string) (*Profile, error) {
	if username == "" || password == "" {
		return nil, errInvalidCredentials
	}
	conn, err := ldap.DialURL(c.url)
	if err != nil {
		return nil, fmt.Errorf("ldap dial: %w", err)
	}
	defer conn.Close()

	if err := conn.Bind(c.bindDN, c.bindPassword); err != nil {
		return nil, fmt.Errorf("ldap service bind: %w", err)
	}
	res, err := conn.Search(ldap.NewSearchRequest(
		c.baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 5, false,
		fmt.Sprintf("(%s=%s)", c.userAttr, ldap.EscapeFilter(username)),
		[]string{"dn", "uid", "cn", "mail", "telephoneNumber", "departmentNumber", "physicalDeliveryOfficeName"}, nil))
	if err != nil {
		return nil, fmt.Errorf("ldap search: %w", err)
	}
	if len(res.Entries) != 1 {
		return nil, errInvalidCredentials
	}
	e := res.Entries[0]
	if err := conn.Bind(e.DN, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, errInvalidCredentials
		}
		return nil, fmt.Errorf("ldap user bind: %w", err)
	}
	return &Profile{
		Username:    e.GetAttributeValue(c.userAttr),
		DisplayName: e.GetAttributeValue("cn"),
		Email:       e.GetAttributeValue("mail"),
		Extension:   e.GetAttributeValue("telephoneNumber"),
		Department:  e.GetAttributeValue("departmentNumber"),
		SiteCode:    e.GetAttributeValue("physicalDeliveryOfficeName"),
	}, nil
}
