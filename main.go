package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

var content embed.FS

// URLMapping stores
type URLMapping struct {
	ShortURL    string    `json:"short_url"`
	LongURL     string    `json:"long_url"`
	Created     time.Time `json:"created"`
	Visits      int       `json:"visits"`
	LastVisited time.Time `json:"last_visited,omitempty"`
}

type URLShortener struct {
	URLs  []URLMapping `json:"urls"`
	mutex sync.RWMutex
}

type PageData struct {
	URLs    []URLMapping
	Message string
	Error   string
}

const (
	characters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	length     = 6
	dataFile   = "urls.json"
)

var (
	shortener URLShortener
	tmpl      *template.Template
)

func init() {
	// Parse templates at startup
	var err error
	tmpl, err = template.ParseFS(content, "templates/*.html")
	if err != nil {
		log.Fatal(err)
	}
}

func generateShortURL() string {
	rand.Seed(time.Now().UnixNano())
	result := make([]byte, length)
	for i := range result {
		result[i] = characters[rand.Intn(len(characters))]
	}
	return string(result)
}

func (s *URLShortener) saveToFile() error {
	data, err := json.MarshalIndent(s, "", "    ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(dataFile, data, 0644)
}

func (s *URLShortener) loadFromFile() error {
	if _, err := os.Stat(dataFile); os.IsNotExist(err) {
		return nil
	}

	data, err := ioutil.ReadFile(dataFile)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, s)
}

func validateURL(rawURL string) error {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if parsedURL.Scheme == "" {
		return fmt.Errorf("URL must include scheme (http:// or https://)")
	}
	if parsedURL.Host == "" {
		return fmt.Errorf("invalid URL: missing host")
	}
	return nil
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		redirectHandler(w, r)
		return
	}

	shortener.mutex.RLock()
	data := PageData{
		URLs: shortener.URLs,
	}
	shortener.mutex.RUnlock()

	tmpl.ExecuteTemplate(w, "index.html", data)
}

func shortenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	longURL := r.FormValue("url")
	if longURL == "" {
		http.Error(w, "URL parameter is required", http.StatusBadRequest)
		return
	}

	if err := validateURL(longURL); err != nil {
		data := PageData{
			URLs:  shortener.URLs,
			Error: err.Error(),
		}
		tmpl.ExecuteTemplate(w, "index.html", data)
		return
	}

	shortener.mutex.Lock()
	defer shortener.mutex.Unlock()

	// Check if URL exists
	for _, mapping := range shortener.URLs {
		if mapping.LongURL == longURL {
			data := PageData{
				URLs:    shortener.URLs,
				Message: fmt.Sprintf("URL already shortened: http://localhost:8080/%s", mapping.ShortURL),
			}
			tmpl.ExecuteTemplate(w, "index.html", data)
			return
		}
	}

	shortURL := generateShortURL()
	mapping := URLMapping{
		ShortURL: shortURL,
		LongURL:  longURL,
		Created:  time.Now(),
		Visits:   0,
	}

	shortener.URLs = append(shortener.URLs, mapping)
	if err := shortener.saveToFile(); err != nil {
		log.Printf("Error saving to file: %v", err)
	}

	data := PageData{
		URLs:    shortener.URLs,
		Message: fmt.Sprintf("Shortened URL: http://localhost:8080/%s", shortURL),
	}
	tmpl.ExecuteTemplate(w, "index.html", data)
}

func redirectHandler(w http.ResponseWriter, r *http.Request) {
	shortURL := r.URL.Path[1:]
	if shortURL == "" {
		http.Error(w, "Invalid short URL", http.StatusBadRequest)
		return
	}

	shortener.mutex.Lock()
	defer shortener.mutex.Unlock()

	for i := range shortener.URLs {
		if shortener.URLs[i].ShortURL == shortURL {
			shortener.URLs[i].Visits++
			shortener.URLs[i].LastVisited = time.Now()
			shortener.saveToFile()
			http.Redirect(w, r, shortener.URLs[i].LongURL, http.StatusMovedPermanently)
			return
		}
	}

	http.Error(w, "URL not found", http.StatusNotFound)
}

func main() {
	if err := shortener.loadFromFile(); err != nil {
		log.Printf("Error loading from file: %v", err)
	}

	// Serve static files
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/shorten", shortenHandler)

	fmt.Println("Server starting on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
