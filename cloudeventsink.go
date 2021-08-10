package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type headers struct {
	name   string   `json:"name"`
	values []string `json:"values"`
}
type httpMsgMeta struct {
	contentLength int64     `json:"contentLength"`
	headers       []headers `json:"headers"`
	nsec          int       `json:"nsec"`
	path          string    `json:"path"`
	method        string    `json:"method"`
	host          string    `json:"host"`
}

type httpMsg struct {
	httpMsgMeta
	body []byte
}

var msgs []httpMsg = []httpMsg{}

var msgsLock sync.Mutex

func cloudeventsMain(logger *zap.SugaredLogger) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		if logger.Desugar().Core().Enabled(zapcore.DebugLevel) {
			var sb strings.Builder
			var sep = ""
			for name, values := range r.Header {
				if len(values) > 0 && (name == "X-Request-Id" || strings.HasPrefix(name, "Ce-")) {
					sb.WriteString(sep)
					sb.WriteString(name)
					sb.WriteString("=")
					sb.WriteString(values[0])
					sep = ", "
				}
			}
			logger.Debugf("%s %s  %s  %s ", r.Host, r.Method, r.URL.String(), sb.String())
		}

		switch r.Method {
		case "DELETE":
			msgsLock.Lock()
			defer msgsLock.Unlock()
			msgs = []httpMsg{}
			logger.Debugf("Messages reset to zero.")
		case "GET":
			var lmsgs []httpMsg
			msgsLock.Lock()
			defer msgsLock.Unlock()
			lmsgs = msgs
			// msgs = []httpMsg{}  // would like to clear once sent.

			if strings.EqualFold("/count", r.RequestURI) {
				count := len(lmsgs)
				logger.Debugf("return count:%d", count)
				w.Write([]byte(strconv.Itoa(count)))
				return
			}

			//			/*  need fixin go > 1.13 https://github.ibm.com/coligo/e2e-test-agent/blob/d5ca934ffa269f80319f4d26ce38be97bc59da1a/Dockerfile#L32
			mw := multipart.NewWriter(w)
			w.Header().Set("Content-Type", mw.FormDataContentType())
			for _, lmsg := range lmsgs {
				n := textproto.MIMEHeader{}
				for _, h := range lmsg.headers {
					for _, v := range h.values {
						n.Add(h.name, v)
					}
				}
				n.Add("__EVENT_RECORDER_TIME_NSEC", strconv.Itoa(lmsg.nsec))
				n.Add("__EVENT_RECORDER_CONTENT_LENGTH", strconv.FormatInt(lmsg.contentLength, 10))
				n.Add("__EVENT_RECORDER_PATH", lmsg.path)
				n.Add("__EVENT_RECORDER_METHOD", lmsg.method)
				n.Add("__EVENT_RECORDER_HOST", lmsg.host)

				iow, err := mw.CreatePart(n)
				if err != nil {
					fmt.Printf("error %s", err.Error())
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if _, err = iow.Write(lmsg.body); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
			if err := mw.Close(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			logger.Debugf("Sent %d messages.", len(lmsgs))

			//			*/

		//	w.Header().Set("Content-Type", mw.)
		case "POST":
			msg := httpMsg{}

			msg.path = r.URL.String()
			msg.method = r.Method
			msg.host = r.Host
			if r.Body != nil {
				msg.body, _ = ioutil.ReadAll(r.Body)
				msg.contentLength = r.ContentLength
				msg.nsec = time.Now().Nanosecond()
			}
			msg.headers = make([]headers, 0)
			for name, values := range r.Header {
				h := headers{
					name: name,
				}
				h.values = make([]string, len(values))
				copy(h.values, values)
				msg.headers = append(msg.headers, h)
			}
			msgsLock.Lock()
			defer msgsLock.Unlock()
			msgs = append(msgs, msg)

		}
	})

	logger.Infof("Listening for events on port 8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}

func main() {

	cloudeventsMain(configZL().Sugar())
}

func configZL() *zap.Logger {
	level := os.Getenv("DEBUG_LEVEL")
	if level == "" {
		level = "info" // debug info warn error dpanic panic fatal
	}
	encoding := os.Getenv("DEBUG_ENCODING")
	if encoding == "" {
		encoding = "json" // json, console
	}

	rawJSON := []byte(fmt.Sprintf(`{
	  "level": "%s",
	  "encoding": "%s",
	  "outputPaths": ["stdout"],
	  "errorOutputPaths": ["stderr"],

	  "encoderConfig": {
	    "messageKey": "message",
	    "levelKey": "level",
	    "levelEncoder": "lowercase"
	  }
	}`, level, encoding))

	// #	  "initialFields": {"foo": "bar"},

	var cfg zap.Config
	if err := json.Unmarshal(rawJSON, &cfg); err != nil {
		panic(err)
	}
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	logger, err := cfg.Build()
	if err != nil {
		panic(err)
	}
	return logger
}
