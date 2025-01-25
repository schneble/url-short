package main

import (
	"context"
	// "encoding/json"
	"fmt"
	"html/template"

	// "io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"

	// "sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var mongoClient *mongo.Client
var urlCollection *mongo.Collection

type URLMapping struct {
	ShortURL    string    `bson:"short_url"`
	LongURL     string    `bson:"long_url"`
	Created     time.Time `bson:"created"`
	Visits      int       `bson:"visits"`
	LastVisited time.Time `bson:"last_visited,omitempty"`
}

type URLShortener struct {
	//  mutex sync.RWMutex
}

type PageData struct {
	URLs    []URLMapping
	Message string
	Error   string
}

const (
	characters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	length     = 6
)

var tmpl *template.Template

func connectMongoDB() error {
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		return fmt.Errorf("MONGODB_URI not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return err
	}

	err = client.Ping(ctx, nil)
	if err != nil {
		return err
	}

	mongoClient = client
	urlCollection = client.Database("urlshortener").Collection("urls")
	return nil
}

func init() {
	var err error
	tmpl, err = template.ParseFiles("templates/index.html")
	if err != nil {
		log.Fatalf("Failed to parse templates: %v", err)
	}
}

func generateShortURL() (string, error) {
	rand.Seed(time.Now().UnixNano())
	result := make([]byte, length)
	for i := range result {
		result[i] = characters[rand.Intn(len(characters))]
	}
	return string(result), nil
}

func (s *URLShortener) saveURLMapping(mapping URLMapping) error {
	_, err := urlCollection.InsertOne(context.Background(), mapping)
	return err
}

func (s *URLShortener) getURLMappings() ([]URLMapping, error) {
	var mappings []URLMapping
	cursor, err := urlCollection.Find(context.Background(), bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())

	for cursor.Next(context.Background()) {
		var mapping URLMapping
		if err := cursor.Decode(&mapping); err != nil {
			return nil, err
		}
		mappings = append(mappings, mapping)
	}

	if err := cursor.Err(); err != nil {
		return nil, err
	}

	return mappings, nil
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

	mappings, err := shortener.getURLMappings()
	if err != nil {
		http.Error(w, "Failed to retrieve URL mappings", http.StatusInternalServerError)
		return
	}

	data := PageData{
		URLs: mappings,
	}
	if err := tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
	}
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
			Error: err.Error(),
		}
		if err := tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
			http.Error(w, "Failed to render template", http.StatusInternalServerError)
		}
		return
	}

	shortURL, err := generateShortURL()
	if err != nil {
		http.Error(w, "Failed to generate short URL", http.StatusInternalServerError)
		return
	}

	mapping := URLMapping{
		ShortURL: shortURL,
		LongURL:  longURL,
		Created:  time.Now(),
		Visits:   0,
	}

	if err := shortener.saveURLMapping(mapping); err != nil {
		http.Error(w, "Failed to save URL mapping", http.StatusInternalServerError)
		return
	}

	data := PageData{
		Message: fmt.Sprintf("Shortened URL: http://localhost:8080/%s", shortURL),
	}
	if err := tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
	}
}

func redirectHandler(w http.ResponseWriter, r *http.Request) {
	shortURL := r.URL.Path[1:]
	if shortURL == "" {
		http.Error(w, "Invalid short URL", http.StatusBadRequest)
		return
	}

	mapping, err := findURLMapping(shortURL)
	if err != nil {
		http.Error(w, "URL not found", http.StatusNotFound)
		return
	}

	mapping.Visits++
	mapping.LastVisited = time.Now()
	if err := updateURLMapping(mapping); err != nil {
		http.Error(w, "Failed to update URL mapping", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, mapping.LongURL, http.StatusMovedPermanently)
}

func findURLMapping(shortURL string) (*URLMapping, error) {
	var mapping URLMapping
	err := urlCollection.FindOne(context.Background(), bson.M{"short_url": shortURL}).Decode(&mapping)
	if err != nil {
		return nil, err
	}
	return &mapping, nil
}

func updateURLMapping(mapping *URLMapping) error {
	_, err := urlCollection.UpdateOne(
		context.Background(),
		bson.M{"short_url": mapping.ShortURL},
		bson.M{"$set": bson.M{
			"visits":       mapping.Visits,
			"last_visited": mapping.LastVisited,
		}},
	)
	return err
}

func main() {
	if err := connectMongoDB(); err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer mongoClient.Disconnect(context.Background())

	// Serve static files
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/shorten", shortenHandler)

	fmt.Println("Server starting on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

var shortener = URLShortener{}
