package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/matella/twitch-bot/internal/bot"
	"github.com/matella/twitch-bot/internal/db"
)

// Server represents the admin web server
type Server struct {
	port     string
	database *db.DB
	bot      *bot.Bot
	mux      *http.ServeMux
}

// New creates a new server instance
func New(port string, database *db.DB, bot *bot.Bot) *Server {
	s := &Server{
		port:     port,
		database: database,
		bot:      bot,
		mux:      http.NewServeMux(),
	}

	s.setupRoutes()
	return s
}

// Handler returns the HTTP handler
func (s *Server) Handler() http.Handler {
	return s.mux
}

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() {
	// Static files
	s.mux.Handle("/", http.FileServer(http.Dir("./web/static")))

	// API routes
	s.mux.HandleFunc("/api/health", s.handleHealth)

	// Commands
	s.mux.HandleFunc("/api/commands", s.handleCommands)
	s.mux.HandleFunc("/api/commands/", s.handleCommandDetail)

	// Queue
	s.mux.HandleFunc("/api/queue", s.handleQueue)
	s.mux.HandleFunc("/api/queue/", s.handleQueueDetail)

	// Settings
	s.mux.HandleFunc("/api/settings", s.handleSettings)
}

// Response represents a JSON API response
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// handleHealth returns server health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{
		Success: true,
		Data: map[string]interface{}{
			"status": "ok",
			"bot_running": s.bot.IsRunning(),
		},
	})
}

// handleCommands handles GET (list all) and POST (create new)
func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		commands, err := s.database.GetAllCommands()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Response{Success: true, Data: commands})

	case http.MethodPost:
		var req struct {
			Name     string `json:"name"`
			Response string `json:"response"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Invalid request"})
			return
		}

		if err := s.database.AddCommand(req.Name, req.Response); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		s.database.LogEvent("command_added", req.Name)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(Response{Success: true, Data: req})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleCommandDetail handles GET, PUT, DELETE for individual commands
func (s *Server) handleCommandDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract command name from URL
	name := r.URL.Path[len("/api/commands/"):]
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(Response{Success: false, Error: "Command name required"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		cmd, err := s.database.GetCommand(name)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		if cmd == nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Command not found"})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Response{Success: true, Data: cmd})

	case http.MethodPut:
		var req struct {
			Response string `json:"response"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Invalid request"})
			return
		}

		if err := s.database.UpdateCommand(name, req.Response); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		s.database.LogEvent("command_updated", name)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Response{Success: true})

	case http.MethodDelete:
		if err := s.database.DeleteCommand(name); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		s.database.LogEvent("command_deleted", name)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Response{Success: true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleQueue handles GET (list queue) and DELETE (clear queue)
func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		songs, err := s.database.GetQueue(50)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Response{Success: true, Data: songs})

	case http.MethodDelete:
		if err := s.database.ClearQueue(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		s.database.LogEvent("queue_cleared", "")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Response{Success: true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleQueueDetail handles DELETE for individual queue items
func (s *Server) handleQueueDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Extract song ID from URL
	idStr := r.URL.Path[len("/api/queue/"):]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(Response{Success: false, Error: "Invalid song ID"})
		return
	}

	if err := s.database.RemoveFromQueue(id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
		return
	}

	s.database.LogEvent("song_removed", strconv.Itoa(id))
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(Response{Success: true})
}

// handleSettings handles settings GET/SET
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		// Return basic bot settings
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data: map[string]interface{}{
				"bot_running": s.bot.IsRunning(),
			},
		})

	case http.MethodPost:
		var req struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Invalid request"})
			return
		}

		if err := s.database.SetSetting(req.Key, req.Value); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		s.database.LogEvent("setting_updated", req.Key)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Response{Success: true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
