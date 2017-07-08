package main

import (
	"bufio"
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/pprof"
	"os"
	"time"

	"github.com/elazarl/go-bindata-assetfs"
	"github.com/gorilla/websocket"
	"github.com/nicksnyder/go-i18n/i18n"
	nest "github.com/patrickalin/nest-api-go"
	"github.com/patrickalin/nest-client-go/assembly-assetfs"
)

type httpServer struct {
	nestMessageToHTTP chan nest.Nest
	httpServ              *http.Server
	conn                  *websocket.Conn
	msgJSON               []byte
	templates             map[string]*template.Template
	store                 store
}

type meas struct {
	Timestamp time.Time
	Value     float64
}

type pageLog struct {
	LogTxt string
}

type pageHome struct {
	Websockerurl string
}

type logStru struct {
	Time  string `json:"time"`
	Msg   string `json:"msg"`
	Level string `json:"level"`
	Param string `json:"param"`
	Fct   string `json:"fct"`
}

const logfile = "nest.log"

//listen
func (httpServ *httpServer) listen(context context.Context) {
	go func() {
		for {
			mynest := <-httpServ.nestMessageToHTTP
			var err error

			httpServ.msgJSON, err = json.Marshal(mynest.GetNestStruct())
			checkErr(err, funcName(), "Marshal json Error", "")

			if httpServ.msgJSON == nil {
				logFatal(err, funcName(), "JSON Empty", "")
			}

			if httpServ.conn != nil {
				httpServ.refreshWebsocket()
			}

			logDebug(funcName(), "Listen", string(httpServ.msgJSON))
		}
	}()
}

func (httpServ *httpServer) refreshWebsocket() {
	t := append(httpServ.msgJSON, []byte("SEPARATOR"+httpServ.store.String("temperatureCelsius"))...)
	t = append(t, []byte("SEPARATOR"+httpServ.store.String("windGustkmh"))...)
	err := httpServ.conn.WriteMessage(websocket.TextMessage, t)
	checkErr(err, funcName(), "Impossible to write to websocket", "")
}

// Websocket handler to send data
func (httpServ *httpServer) refreshdata(w http.ResponseWriter, r *http.Request) {
	logDebug(funcName(), "Refresh data Websocket handle", "")

	upgrader := websocket.Upgrader{}

	var err error

	httpServ.conn, err = upgrader.Upgrade(w, r, nil)
	checkErr(err, funcName(), "Upgrade upgrader", "")

	if err = httpServ.conn.WriteMessage(websocket.TextMessage, httpServ.msgJSON); err != nil {
		logFatal(err, funcName(), "Impossible to write to websocket", "")
	}
}

// Websocket handler to send data
func (httpServ *httpServer) refreshHistory(w http.ResponseWriter, r *http.Request) {
	logDebug(funcName(), "Refresh history Websocket handle", "")

	upgrader := websocket.Upgrader{}

	var err error

	httpServ.conn, err = upgrader.Upgrade(w, r, nil)
	checkErr(err, funcName(), "Upgrade upgrader", "")

	httpServ.refreshWebsocket()
}

func getWs(r *http.Request) string {
	if r.TLS == nil {
		return "ws://"
	}
	return "wss://"
}

// Home nest handler
func (httpServ *httpServer) home(w http.ResponseWriter, r *http.Request) {
	logDebug(funcName(), "Home Http handle", "")

	p := pageHome{Websockerurl: getWs(r) + r.Host + "/refreshdata"}
	if err := httpServ.templates["home"].Execute(w, p); err != nil {
		logFatal(err, funcName(), "Execute template home", "")
	}
}

// Home nest handler
func (httpServ *httpServer) history(w http.ResponseWriter, r *http.Request) {
	logDebug(funcName(), "Home History handle", "")

	p := pageHome{Websockerurl: getWs(r) + r.Host + "/refreshhistory"}
	if err := httpServ.templates["history"].Execute(w, p); err != nil {
		logFatal(err, funcName(), "Execute template history", "")
	}
}

// Log handler
func (httpServ *httpServer) log(w http.ResponseWriter, r *http.Request) {
	logDebug(funcName(), "Log Http handle", "")

	p := map[string]interface{}{"logRange": createArrayLog(logfile)}

	err := httpServ.templates["log"].Execute(w, p)
	checkErr(err, funcName(), "Compile template log", "")
}

func getFileServer(dev bool) http.FileSystem {
	if dev {
		return http.Dir("static")
	}
	return &assetfs.AssetFS{Asset: assemblyAssetfs.Asset, AssetDir: assemblyAssetfs.AssetDir, AssetInfo: assemblyAssetfs.AssetInfo, Prefix: "static"}
}

//createWebServer create web server
func createWebServer(in chan nest.Nest, HTTPPort string, HTTPSPort string, translate i18n.TranslateFunc, devel bool, store store) (*httpServer, error) {

	t := make(map[string]*template.Template)
	t["home"] = GetHTMLTemplate("nest", []string{"tmpl/index.html", "tmpl/nest/script.html", "tmpl/nest/body.html", "tmpl/nest/menu.html", "tmpl/header.html", "tmpl/endScript.html"}, map[string]interface{}{"T": translate}, devel)
	t["history"] = GetHTMLTemplate("nest", []string{"tmpl/index.html", "tmpl/history/script.html", "tmpl/history/body.html", "tmpl/history/menu.html", "tmpl/header.html", "tmpl/endScript.html"}, map[string]interface{}{"T": translate}, devel)
	t["log"] = GetHTMLTemplate("nest", []string{"tmpl/index.html", "tmpl/log/script.html", "tmpl/log/body.html", "tmpl/log/menu.html", "tmpl/header.html", "tmpl/endScript.html"}, map[string]interface{}{"T": translate}, devel)

	server := &httpServer{nestMessageToHTTP: in,
		templates: t,
		store:     store}

	fs := http.FileServer(getFileServer(devel))

	s := http.NewServeMux()

	s.Handle("/static/", http.StripPrefix("/static/", fs))
	s.Handle("/favicon.ico", fs)
	s.HandleFunc("/", server.home)
	s.HandleFunc("/refreshdata", server.refreshdata)
	s.HandleFunc("/refreshhistory", server.refreshHistory)
	s.HandleFunc("/log", server.log)
	s.HandleFunc("/history", server.history)
	s.HandleFunc("/debug/pprof/", pprof.Index)
	s.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	s.HandleFunc("/debug/pprof/profile", pprof.Profile)
	s.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	s.HandleFunc("/debug/pprof/trace", pprof.Trace)

	h := &http.Server{Addr: HTTPPort, Handler: s}
	go func() {
		err := h.ListenAndServe()
		checkErr(err, funcName(), "Error when I create the server HTTP (don't forget ':')", "")
	}()

	hs := &http.Server{Addr: HTTPSPort, Handler: s}
	go func() {
		err := hs.ListenAndServeTLS("server.crt", "server.key")
		checkErr(err, funcName(), "Error when I create the server HTTPS (don't forget ':')", "")
	}()

	logInfo(funcName(), "Server HTTP listen on port", HTTPPort)
	logInfo(funcName(), "Server HTTPS listen on port", HTTPSPort)

	server.httpServ = h
	return server, nil
}

func createArrayLog(logFile string) (logRange []logStru) {
	file, err := os.Open(logFile)
	checkErr(err, funcName(), "Imposible to open file", "nest.log")

	defer file.Close()
	scanner := bufio.NewScanner(file)

	var tt logStru
	for scanner.Scan() {
		json.Unmarshal([]byte(scanner.Text()), &tt)
		checkErr(err, funcName(), "Impossible to unmarshall log", scanner.Text())

		logRange = append(logRange, tt)
	}

	scanner.Err()
	checkErr(err, funcName(), "Scanner Err", "")

	return logRange
}
