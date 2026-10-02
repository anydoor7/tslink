package server

import (
	"html/template"
	"net/http"

	"golang.org/x/text/language"
)

type guestPage struct {
	Lang, Title, Help, Retry, Button, CSRF string
}

func guestPageText(r *http.Request, retry bool, csrf string) guestPage {
	p := guestPage{Lang: "en", Title: "Access unavailable.", Help: "Reopen the original link or ask the person who sent it.", Button: "Continue", CSRF: csrf}
	tags, _, err := language.ParseAcceptLanguage(r.Header.Get("Accept-Language"))
	if err == nil {
		_, index, _ := language.NewMatcher([]language.Tag{language.English, language.Chinese}).Match(tags...)
		if index == 1 {
			p.Lang = "zh"
			p.Title = "Access unavailable. / 暂时无法访问"
			p.Help = "Reopen the original link or ask the person who sent it. / 请重新打开原始链接，或联系发送链接的人。"
			p.Button = "Continue / 继续"
		}
	}
	if retry {
		p.Retry = "Please try again."
		if p.Lang == "zh" {
			p.Retry += " / 请重试。"
		}
	}
	return p
}

const guestPageStyle = `<style>body{margin:0;padding:32px 24px;font:16px/1.5 system-ui,sans-serif;color:#1f2937;background:#f8fafc}main{max-width:400px;margin:32px auto}h1{font-size:24px;line-height:1.3}form{display:grid;gap:16px}label{display:grid;gap:8px}input,button{box-sizing:border-box;width:100%;min-height:44px;padding:10px 12px;font:inherit;border:1px solid #64748b;border-radius:6px}button{background:#1f2937;color:white}p{overflow-wrap:anywhere}</style>`

var guestDeniedPage = template.Must(template.New("denied").Parse(`<!doctype html><html lang="{{.Lang}}"><head><meta name="referrer" content="no-referrer"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Access unavailable</title>` + guestPageStyle + `</head><body><main><h1>{{.Title}}</h1><p>{{.Help}}</p></main></body></html>`))

var guestForm = template.Must(template.New("pin").Parse(`<!doctype html><html lang="{{.Lang}}"><head><meta name="referrer" content="no-referrer"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Enter PIN</title>` + guestPageStyle + `</head><body><main><h1>Enter PIN{{if eq .Lang "zh"}} / 输入 PIN{{end}}</h1>{{if .Retry}}<p role="alert">{{.Retry}}</p>{{end}}<form method="post" action="/guest/pin"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>PIN <input name="pin" type="password" inputmode="numeric" autocomplete="off" required maxlength="64"></label><button>{{.Button}}</button></form></main></body></html>`))
