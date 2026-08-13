package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func getUser(w http.ResponseWriter, r *http.Request) {
	user := User{ID: 1, Name: "Nurul"}
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(user); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func main() {
	http.HandleFunc("/user", getUser)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
