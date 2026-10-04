// soap client for rccservice, X-RCC-Auth has to match RCCSharedSecret in the registry
package rcc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const soapNS = "http://roblox.com/"

type Client struct {
	Name   string
	URL    string
	Secret string
	http   *http.Client
	slot   chan struct{}
}

// one call at a time per rcc, it crashes when poked mid render
var slots sync.Map

func slotFor(url string) chan struct{} {
	v, _ := slots.LoadOrStore(url, make(chan struct{}, 1))
	return v.(chan struct{})
}

func NewClient(name, url, secret string) *Client {
	url = strings.TrimRight(url, "/") + "/"
	return &Client{
		Name:   name,
		URL:    url,
		Secret: secret,
		http: &http.Client{
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 4,
			},
		},
		slot: slotFor(url),
	}
}

type LuaValue struct {
	Type  string
	Value string
}

func String(s string) LuaValue {
	return LuaValue{
		Type:  "LUA_TSTRING",
		Value: s,
	}
}

func Number(n int64) LuaValue {
	return LuaValue{
		Type:  "LUA_TNUMBER",
		Value: fmt.Sprint(n),
	}
}

func Bool(b bool) LuaValue {
	return LuaValue{
		Type:  "LUA_TBOOLEAN",
		Value: fmt.Sprint(b),
	}
}

func FirstString(vals []LuaValue) (string, bool) {
	if len(vals) == 0 {
		return "", false
	}
	return vals[0].Value, true
}

type Error struct {
	Client string
	Action string
	Fault  bool // rcc answered, with a faultstring
	Msg    string
}

func (c *Client) fail(action, msg string) *Error {
	return &Error{
		Client: c.Name,
		Action: action,
		Msg:    msg,
	}
}

func (e *Error) Error() string {
	kind := "unreachable"
	if e.Fault {
		kind = "fault"
	}
	return fmt.Sprintf("rcc %s %s %s: %s", e.Client, e.Action, kind, e.Msg)
}

// rcc was reachable and said no
func IsFault(err error) bool {
	var re *Error
	return errors.As(err, &re) && re.Fault
}

func IsError(err error) bool {
	var re *Error
	return errors.As(err, &re)
}

func (c *Client) BatchJob(ctx context.Context, jobID, name, script string, expiration time.Duration, args ...LuaValue) ([]LuaValue, error) {
	var b bytes.Buffer
	b.WriteString(`<BatchJob xmlns="` + soapNS + `">`)
	writeJob(&b, jobID, expiration)
	writeScript(&b, name, script, args)
	b.WriteString(`</BatchJob>`)
	doc, err := c.call(ctx, "BatchJob", b.String())
	if err != nil {
		return nil, err
	}
	return doc.luaValues("BatchJobResult"), nil
}

func (c *Client) OpenJob(ctx context.Context, jobID, name, script string, expiration time.Duration, args ...LuaValue) ([]LuaValue, error) {
	var b bytes.Buffer
	b.WriteString(`<OpenJob xmlns="` + soapNS + `">`)
	writeJob(&b, jobID, expiration)
	writeScript(&b, name, script, args)
	b.WriteString(`</OpenJob>`)
	doc, err := c.call(ctx, "OpenJob", b.String())
	if err != nil {
		return nil, err
	}
	return doc.luaValues("OpenJobResult"), nil
}

func (c *Client) Execute(ctx context.Context, jobID, name, script string, args ...LuaValue) ([]LuaValue, error) {
	var b bytes.Buffer
	b.WriteString(`<Execute xmlns="` + soapNS + `">`)
	b.WriteString(`<jobID>` + esc(jobID) + `</jobID>`)
	writeScript(&b, name, script, args)
	b.WriteString(`</Execute>`)
	doc, err := c.call(ctx, "Execute", b.String())
	if err != nil {
		return nil, err
	}
	return doc.luaValues("ExecuteResult"), nil
}

func (c *Client) CloseJob(ctx context.Context, jobID string) error {
	_, err := c.call(ctx, "CloseJob", `<CloseJob xmlns="`+soapNS+`"><jobID>`+esc(jobID)+`</jobID></CloseJob>`)
	return err
}

func (c *Client) GetAllJobs(ctx context.Context) ([]string, error) {
	doc, err := c.call(ctx, "GetAllJobs", `<GetAllJobs xmlns="`+soapNS+`" />`)
	if err != nil {
		return nil, err
	}
	var ids []string
	doc.walk(func(n *xmlNode) {
		if n.local() == "id" {
			ids = append(ids, strings.TrimSpace(n.Text))
		}
	})
	return ids, nil
}

func (c *Client) RenewLease(ctx context.Context, jobID string, expiration time.Duration) error {
	_, err := c.call(ctx, "RenewLease",
		`<RenewLease xmlns="`+soapNS+`"><jobID>`+esc(jobID)+`</jobID>`+
			`<expirationInSeconds>`+fmt.Sprint(int(expiration.Seconds()))+`</expirationInSeconds></RenewLease>`)
	return err
}

// cheapest call that proves its up and the secret is right
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.GetAllJobs(ctx)
	return err
}

func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func writeJob(b *bytes.Buffer, jobID string, expiration time.Duration) {
	b.WriteString(`<job><id>` + esc(jobID) + `</id>`)
	b.WriteString(`<expirationInSeconds>` + fmt.Sprint(int(expiration.Seconds())) + `</expirationInSeconds>`)
	b.WriteString(`<category>1</category><cores>1</cores></job>`)
}

func writeScript(b *bytes.Buffer, name, script string, args []LuaValue) {
	b.WriteString(`<script><name>` + esc(name) + `</name>`)
	b.WriteString(`<script>` + esc(script) + `</script>`)
	if len(args) > 0 {
		b.WriteString(`<arguments>`)
		for _, a := range args {
			b.WriteString(`<LuaValue><type>` + esc(a.Type) + `</type><value>` + esc(a.Value) + `</value></LuaValue>`)
		}
		b.WriteString(`</arguments>`)
	}
	b.WriteString(`</script>`)
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (c *Client) call(ctx context.Context, action, body string) (*xmlNode, error) {
	envelope := `<?xml version="1.0" encoding="utf-8"?>` +
		`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
		body +
		`</soap:Body></soap:Envelope>`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, strings.NewReader(envelope))
	if err != nil {
		return nil, c.fail(action, err.Error())
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", soapNS+action)
	req.Header.Set("X-RCC-Auth", c.Secret)

	select {
	case c.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, c.fail(action, "waiting for rcc: "+ctx.Err().Error())
	}
	resp, err := c.http.Do(req)
	if err != nil {
		<-c.slot
		return nil, c.fail(action, err.Error())
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	resp.Body.Close()
	<-c.slot
	if err != nil {
		return nil, c.fail(action, "reading response: "+err.Error())
	}

	var doc xmlNode
	if err := xml.Unmarshal(raw, &doc); err != nil {
		msg := fmt.Sprintf("unparsable soap (HTTP %d): %s", resp.StatusCode, err)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			msg = fmt.Sprintf("HTTP %d, X-RCC-Auth probably does not match RCCSharedSecret", resp.StatusCode)
		}
		return nil, c.fail(action, msg)
	}
	if fault := doc.find("faultstring"); fault != nil {
		return nil, &Error{
			Client: c.Name,
			Action: action,
			Fault:  true,
			Msg:    strings.TrimSpace(fault.Text),
		}
	}
	if resp.StatusCode/100 != 2 {
		return nil, c.fail(action, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
	return &doc, nil
}

// some builds put values in <LuaValue>, some straight in the result
type xmlNode struct {
	XMLName  xml.Name
	Text     string    `xml:",chardata"`
	Children []xmlNode `xml:",any"`
}

func (n *xmlNode) local() string {
	return n.XMLName.Local
}

func (n *xmlNode) walk(fn func(*xmlNode)) {
	fn(n)
	for i := range n.Children {
		n.Children[i].walk(fn)
	}
}

func (n *xmlNode) find(local string) *xmlNode {
	var out *xmlNode
	n.walk(func(x *xmlNode) {
		if out == nil && x.local() == local {
			out = x
		}
	})
	return out
}

func (n *xmlNode) child(local string) *xmlNode {
	for i := range n.Children {
		if n.Children[i].local() == local {
			return &n.Children[i]
		}
	}
	return nil
}

func (n *xmlNode) luaValues(resultName string) []LuaValue {
	var out []LuaValue
	n.walk(func(x *xmlNode) {
		if x.local() != resultName && x.local() != "LuaValue" {
			return
		}
		t := x.child("type")
		if t == nil {
			return
		}
		v := LuaValue{Type: strings.TrimSpace(t.Text)}
		if val := x.child("value"); val != nil {
			v.Value = val.Text
		}
		out = append(out, v)
	})
	return out
}
