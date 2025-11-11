package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
	"github.com/yuin/goldmark"
)

type PageData struct {
	Content template.HTML
}

var logger *slog.Logger

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type Hub struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan []byte
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
}

func newHub() *Hub {
	return &Hub{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
	}
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.Close()
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				err := client.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					logger.Error("error writing to client, removing", "err", err)
					client.Close()
					delete(h.clients, client)
				}
			}
		}
	}
}

func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("failed to upgrade websocket", "err", err)
		return
	}

	hub.register <- conn

	go func(c *websocket.Conn) {
		defer func() {
			hub.unregister <- c
			c.Close()
		}()

		for {
			if _, _, err := c.NextReader(); err != nil {
				break
			}
		}
	}(conn)
}

func main() {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})
	logger = slog.New(handler)

	// single-generation flags
	markdownDocument := flag.String("markdown", "post.md", "the input markdown post file")
	templateFile := flag.String("template", "template.html", "the template file for the markdown document")
	outputFile := flag.String("output", "output.html", "the output html file")

	// live reload flags
	watch := flag.Bool("watch", false, "watch for changes and reload the template")
	serve := flag.Bool("serve", false, "enable server with live reload")
	port := flag.String("port", "8080", "port for the server")

	flag.Parse()

	if markdownDocument == nil || *markdownDocument == "" {
		logger.Error("the markdown document flag must be present")
		return
	}

	if templateFile == nil || *templateFile == "" {
		logger.Error("the template file must be present")
		return
	}

	if outputFile == nil || *outputFile == "" {
		logger.Error("the output file must be present")
		return
	}

	if *serve {
		runServer(markdownDocument, templateFile, outputFile, port)
	} else {
		runCli(markdownDocument, templateFile, outputFile, watch)
	}
}

func runCli(markdownDocument, templateFile, outputFile *string, watch *bool) {
	if !*watch {
		err := buildDocument(markdownDocument, templateFile, outputFile)
		if err != nil {
			logger.Error("there was an error building the markdown document")
			return
		}

		logger.Info("the template was successfully rendered", "input", *markdownDocument, "template", *templateFile, "output", *outputFile)

		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Error("there was an error creating the file watcher")
		return
	}
	defer watcher.Close()

	go startWatcher(watcher, markdownDocument, templateFile, outputFile, nil)

	if err := buildDocument(markdownDocument, templateFile, outputFile); err != nil {
		logger.Error("error performing initial build", "err", err)
	} else {
		logger.Info("initial build successful")
	}

	if err := watcher.Add(*markdownDocument); err != nil {
		logger.Error("error adding markdown file to watcher", "err", err)
		return
	}
	if err := watcher.Add(*templateFile); err != nil {
		logger.Error("error adding template file to watcher", "err", err)
		return
	}

	logger.Info("watching for changes...")
	<-make(chan struct{})
}

func runServer(markdownDocument, templateFile, outputFile, port *string) {
	hub := newHub()
	go hub.run()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Error("there was an error creating the file watcher")
		return
	}
	defer watcher.Close()

	go startWatcher(watcher, markdownDocument, templateFile, outputFile, hub)

	if err := buildDocument(markdownDocument, templateFile, outputFile); err != nil {
		logger.Error("error performing initial build", "err", err)
	} else {
		logger.Info("initial build successful")
	}

	if err := watcher.Add(*markdownDocument); err != nil {
		logger.Error("error adding markdown file to watcher", "err", err)
		return
	}

	if err := watcher.Add(*templateFile); err != nil {
		logger.Error("error adding template file to watcher", "err", err)
		return
	}

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})

	fs := http.FileServer(http.Dir("."))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			html, err := os.ReadFile(*outputFile)
			if err != nil {
				http.Error(w, "Could not read output file: "+err.Error(), http.StatusInternalServerError)
				return
			}

			script := fmt.Sprintf(`<script>
	let socket = new WebSocket("ws://%s/ws");
	socket.onmessage = function(event) {
		if (event.data === "reload") {
			location.reload();
		}
	};
	socket.onclose = function(event) {
		console.log("Live reload socket closed. Reloading page to try reconnecting...");
		setTimeout(() => location.reload(), 2000);
	};
</script>`, r.Host)

			injectedHTML := strings.Replace(string(html), "</body>", script+"</body>", 1)
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(injectedHTML))

			return
		}

		fs.ServeHTTP(w, r)
	})

	logger.Info("starting server, watching for changes...", "address", "http://localhost:"+*port)

	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		logger.Error("server failed to start", "err", err)
	}
}

func startWatcher(watcher *fsnotify.Watcher, markdownDocument, templateFile, outputFile *string, hub *Hub) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if !(event.Has(fsnotify.Write) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove)) {
				continue
			}

			if event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove) {
				time.Sleep(100 * time.Millisecond)
			}

			logger.Info("change detected, rebuilding...", "file", event.Name, "op", event.Op.String())

			if err := buildDocument(markdownDocument, templateFile, outputFile); err != nil {
				if !(event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove)) {
					logger.Error("error rebuilding document", "err", err)
				}
			} else {
				logger.Info("rebuild successful")
				if hub != nil {
					hub.broadcast <- []byte("reload")
				}
			}

			if event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove) {
				watcher.Add(*markdownDocument)
				watcher.Add(*templateFile)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}

			logger.Error("error watching files", "err", err)
		}
	}
}

func buildDocument(markdownDocument *string, templateFile *string, outputFile *string) error {
	markdownContent, err := os.ReadFile(*markdownDocument)
	if err != nil {
		logger.Error("there was an error parsing the markdown content", "err", err)
		return err
	}

	var buf bytes.Buffer
	if err := goldmark.Convert(markdownContent, &buf); err != nil {
		logger.Error("error converting markdown into html", "err", err)
		return err
	}

	tmpl, err := template.ParseFiles(*templateFile)
	if err != nil {
		logger.Error("error parsing template file", "err", err)
		return err
	}

	output, err := os.Create(*outputFile)
	if err != nil {
		logger.Error("error creating output file", "err", err)
		return err
	}
	defer output.Close()

	data := PageData{
		Content: template.HTML(buf.String()),
	}

	if err := tmpl.Execute(output, data); err != nil {
		logger.Error("error rendering template with markdown", "err", err)
		return err
	}

	return nil
}
