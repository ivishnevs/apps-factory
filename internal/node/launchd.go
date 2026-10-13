package node

import (
	"io"
	"text/template"
)

// Launchd is a per-user launchd agent running `factory-node run` with KeepAlive (node-agent D7).
type Launchd struct {
	Label, Executable, Config, WorkingDir, Log string
	Env                                        map[string]string
}

var plist = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>{{.Label}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.Executable}}</string>
    <string>run</string>{{if .Config}}
    <string>-config</string>
    <string>{{.Config}}</string>{{end}}
  </array>
  <key>WorkingDirectory</key><string>{{.WorkingDir}}</string>{{if .Env}}
  <key>EnvironmentVariables</key>
  <dict>{{range $k, $v := .Env}}
    <key>{{$k}}</key><string>{{$v}}</string>{{end}}
  </dict>{{end}}
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>ProcessType</key><string>Interactive</string>
  <key>ExitTimeOut</key><integer>30</integer>
  <key>StandardOutPath</key><string>{{.Log}}</string>
  <key>StandardErrorPath</key><string>{{.Log}}</string>
</dict>
</plist>
`))

// Render writes the plist; values are XML-escaped.
func (l Launchd) Render(w io.Writer) error {
	esc := Launchd{Label: xml(l.Label), Executable: xml(l.Executable), Config: xml(l.Config), WorkingDir: xml(l.WorkingDir), Log: xml(l.Log)}
	if len(l.Env) > 0 {
		esc.Env = map[string]string{}
		for k, v := range l.Env {
			esc.Env[xml(k)] = xml(v)
		}
	}
	return plist.Execute(w, esc)
}

func xml(s string) string { return template.HTMLEscapeString(s) }
