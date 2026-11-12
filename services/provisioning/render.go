package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"text/template"
)

// phone is everything a configuration file needs.
type phone struct {
	Vendor      string
	Extension   string
	DisplayName string
	SIPUser     string
	Password    string
	Server      string
	Port        int
}

// clean strips anything that could break out of a line-oriented config value (CR/LF injection).
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

var funcs = template.FuncMap{"clean": clean, "xml": func(s string) string { return xmlEscape(clean(s)) }}

// The templates below follow the vendors' documented auto-provisioning formats. They must be validated
// against the exact phone model and firmware before a mass roll-out (see docs/provisioning.md).
var templates = map[string]string{
	"yealink": `#!version:1.0.0.1
account.1.enable = 1
account.1.label = {{clean .Extension}}
account.1.display_name = {{clean .DisplayName}}
account.1.auth_name = {{clean .SIPUser}}
account.1.user_name = {{clean .Extension}}
account.1.password = {{clean .Password}}
account.1.sip_server.1.address = {{clean .Server}}
account.1.sip_server.1.port = {{.Port}}
account.1.sip_server.1.transport_type = 0
account.1.sip_server.1.expires = 3600
account.1.nat.udp_update_enable = 1
account.1.codec.pcmu.enable = 1
account.1.codec.pcmu.priority = 1
account.1.codec.pcma.enable = 1
account.1.codec.pcma.priority = 2
account.1.dtmf.type = 1
local_time.time_zone = +3
static.auto_provision.repeat.enable = 1
static.auto_provision.repeat.minutes = 1440
`,
	"grandstream": `<?xml version="1.0" encoding="UTF-8"?>
<gs_provision version="1">
  <config version="1">
    <P271>1</P271>
    <P47>{{xml .Server}}:{{.Port}}</P47>
    <P35>{{xml .Extension}}</P35>
    <P36>{{xml .SIPUser}}</P36>
    <P34>{{xml .Password}}</P34>
    <P3>{{xml .DisplayName}}</P3>
    <P57>0</P57>
    <P58>8</P58>
    <P79>101</P79>
    <P52>1</P52>
  </config>
</gs_provision>
`,
	"fanvil": `<<VOIP CONFIG FILE>>Version:2.0002

--SIP1 Config--
SIP1 Phone Number :{{clean .Extension}}
SIP1 Display Name :{{clean .DisplayName}}
SIP1 Register Addr :{{clean .Server}}
SIP1 Register Port :{{.Port}}
SIP1 Register User :{{clean .SIPUser}}
SIP1 Register Pswd :{{clean .Password}}
SIP1 Register TTL :3600
SIP1 Enable Reg :1
`,
}

var compiled = func() map[string]*template.Template {
	m := map[string]*template.Template{}
	for k, v := range templates {
		m[k] = template.Must(template.New(k).Funcs(funcs).Parse(v))
	}
	m["generic"] = m["yealink"]
	return m
}()

func contentType(vendor string) string {
	if vendor == "grandstream" {
		return "text/xml; charset=utf-8"
	}
	return "text/plain; charset=utf-8"
}

func render(p phone) (string, error) {
	t, ok := compiled[p.Vendor]
	if !ok {
		return "", fmt.Errorf("unsupported vendor %q", p.Vendor)
	}
	var b bytes.Buffer
	if err := t.Execute(&b, p); err != nil {
		return "", err
	}
	return b.String(), nil
}
