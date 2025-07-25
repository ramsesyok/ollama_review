package handlers

import (
	"encoding/json"
	"net/http"
	"sync"
)

// User represents a simple user entity.
type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// UsersHandler provides HTTP endpoints for managing users.
type UsersHandler struct {
	mu     sync.Mutex
	nextID int
	users  map[int]User
}

// NewUsersHandler returns a new UsersHandler with an empty user list.
func NewUsersHandler() *UsersHandler {
	return &UsersHandler{users: make(map[int]User), nextID: 1}
}

// ServeHTTP dispatches HTTP requests to the appropriate method handler.
func (h *UsersHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listUsers(w, r)
	case http.MethodPost:
		h.createUser(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// listUsers writes all users as JSON.
func (h *UsersHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()

	list := make([]User, 0, len(h.users))
	for _, u := range h.users {
		list = append(list, u)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// createUser reads a user from request body and stores it.
func (h *UsersHandler) createUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if input.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	id := h.nextID
	h.nextID++
	user := User{ID: id, Name: input.Name}
	h.users[id] = user
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}
