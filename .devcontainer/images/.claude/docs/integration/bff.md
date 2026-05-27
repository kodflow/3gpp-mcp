# Backend for Frontend (BFF) Pattern

> A dedicated backend API for each client type (web, mobile, IoT).

---

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                  BACKEND FOR FRONTEND                            │
│                                                                  │
│  Without BFF:                    With BFF:                       │
│                                                                  │
│  ┌────┐ ┌────┐ ┌────┐           ┌────┐ ┌────┐ ┌────┐            │
│  │Web │ │iOS │ │IoT │           │Web │ │iOS │ │IoT │            │
│  └──┬─┘ └─┬──┘ └─┬──┘           └─┬──┘ └─┬──┘ └─┬──┘            │
│     │     │      │                │      │      │               │
│     │     │      │                ▼      ▼      ▼               │
│     │     │      │             ┌─────┐┌─────┐┌─────┐            │
│     │     │      │             │BFF-W││BFF-M││BFF-I│            │
│     │     │      │             └──┬──┘└──┬──┘└──┬──┘            │
│     │     │      │                │      │      │               │
│     └─────┼──────┘                └──────┼──────┘               │
│           │                              │                       │
│           ▼                              ▼                       │
│     ┌──────────┐                   ┌──────────┐                 │
│     │  Generic │                   │ Services │                 │
│     │   API    │                   │          │                 │
│     └──────────┘                   └──────────┘                 │
│                                                                  │
│  Problems:                       Advantages:                     │
│  - Over-fetching               - Optimized data                  │
│  - Under-fetching              - Adapted format                  │
│  - Compromises for all         - Fewer round-trips               │
└─────────────────────────────────────────────────────────────────┘
```

---

## Use Cases by Platform

| Client | Specific Needs |
|--------|----------------|
| **Web** | Pagination, SEO metadata, large payloads OK |
| **Mobile** | Compact payloads, offline support, battery |
| **IoT** | Minimal data, binary protocols, low bandwidth |
| **Watch** | Very compact, notifications, health data |

---

## Go Implementation

### Web BFF

```go
package bff

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// WebProductList represents the web-optimized product list response.
type WebProductList struct {
	Products   []WebProduct `json:"products"`
	Pagination Pagination   `json:"pagination"`
	Filters    []Filter     `json:"filters"`
	SEO        SEOMetadata  `json:"seo"`
}

// WebProduct represents a product with full details for web.
type WebProduct struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Price        float64  `json:"price"`
	Images       []string `json:"images"`
	Rating       Rating   `json:"rating"`
	Availability string   `json:"availability"`
	Breadcrumb   []string `json:"breadcrumb"`
}

// Pagination represents pagination metadata.
type Pagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"pageSize"`
	Total      int `json:"total"`
	TotalPages int `json:"totalPages"`
}

// Filter represents a product filter option.
type Filter struct {
	Name    string   `json:"name"`
	Options []string `json:"options"`
}

// SEOMetadata represents SEO-related metadata.
type SEOMetadata struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	CanonicalURL string `json:"canonicalUrl"`
}

// Rating represents product rating information.
type Rating struct {
	Average float64 `json:"average"`
	Count   int     `json:"count"`
}

// WebBFF implements the backend for frontend for web clients.
type WebBFF struct {
	mux    *http.ServeMux
	server *http.Server
	logger *slog.Logger
}

// NewWebBFF creates a new web BFF.
func NewWebBFF(port int, logger *slog.Logger) *WebBFF {
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	bff := &WebBFF{
		mux:    mux,
		logger: logger,
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
		},
	}

	bff.setupRoutes()
	return bff
}

func (b *WebBFF) setupRoutes() {
	// Product list with all info for SEO and web UX
	b.mux.HandleFunc("/products", b.handleProducts)

	// Full product detail for web
	b.mux.HandleFunc("/products/", b.handleProductDetail)
}

func (b *WebBFF) handleProducts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page := getIntParam(r, "page", 1)
	pageSize := getIntParam(r, "pageSize", 20)
	category := r.URL.Query().Get("category")
	sort := r.URL.Query().Get("sort")

	// Aggregate from multiple services in parallel
	type result struct {
		products   *ProductsResult
		filters    []Filter
		breadcrumb []string
		err        error
	}

	ch := make(chan result, 1)

	go func() {
		var r result
		products, filters, breadcrumb, err := b.fetchAggregatedData(ctx, page, pageSize, category, sort)
		r.products = products
		r.filters = filters
		r.breadcrumb = breadcrumb
		r.err = err
		ch <- r
	}()

	res := <-ch
	if res.err != nil {
		b.writeError(w, http.StatusInternalServerError, "failed to fetch products")
		return
	}

	webProducts := make([]WebProduct, len(res.products.Items))
	for i, p := range res.products.Items {
		webProducts[i] = WebProduct{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description, // Full description for web
			Price:       p.Price,
			Images:      p.Images, // All images
			Rating: Rating{
				Average: p.Rating.Average,
				Count:   p.Rating.Count,
			},
			Availability: b.getAvailability(p.Stock),
			Breadcrumb:   res.breadcrumb,
		}
	}

	totalPages := (res.products.Total + pageSize - 1) / pageSize

	response := WebProductList{
		Products: webProducts,
		Pagination: Pagination{
			Page:       page,
			PageSize:   pageSize,
			Total:      res.products.Total,
			TotalPages: totalPages,
		},
		Filters: res.filters,
		SEO: SEOMetadata{
			Title:        fmt.Sprintf("%s Products - My Store", category),
			Description:  fmt.Sprintf("Browse %d %s products", res.products.Total, category),
			CanonicalURL: fmt.Sprintf("/products?category=%s", category),
		},
	}

	b.writeJSON(w, response)
}

func (b *WebBFF) handleProductDetail(w http.ResponseWriter, r *http.Request) {
	// Extract product ID from path
	id := r.URL.Path[len("/products/"):]
	ctx := r.Context()

	type result struct {
		product *Product
		reviews *ReviewsResult
		related []Product
		err     error
	}

	ch := make(chan result, 1)

	go func() {
		var r result
		product, reviews, related, err := b.fetchProductDetails(ctx, id)
		r.product = product
		r.reviews = reviews
		r.related = related
		r.err = err
		ch <- r
	}()

	res := <-ch
	if res.err != nil {
		b.writeError(w, http.StatusInternalServerError, "failed to fetch product")
		return
	}

	response := map[string]interface{}{
		"id":          res.product.ID,
		"name":        res.product.Name,
		"description": res.product.Description,
		"price":       res.product.Price,
		"images":      res.product.Images,
		"rating":      res.product.Rating,
		"reviews":     res.reviews.Items,
		"reviewsSummary": res.reviews.Summary,
		"relatedProducts": res.related,
		"seo": SEOMetadata{
			Title:       fmt.Sprintf("%s - My Store", res.product.Name),
			Description: truncate(res.product.Description, 160),
		},
	}

	b.writeJSON(w, response)
}

// Helper types for service responses
type ProductsResult struct {
	Items []Product
	Total int
}

type Product struct {
	ID          string
	Name        string
	Description string
	Price       float64
	Images      []string
	Rating      Rating
	Stock       int
}

type ReviewsResult struct {
	Items   []Review
	Summary ReviewSummary
}

type Review struct {
	ID        string
	Rating    float64
	Text      string
	Author    string
	CreatedAt time.Time
}

type ReviewSummary struct {
	Average float64
	Count   int
}

func (b *WebBFF) fetchAggregatedData(ctx context.Context, page, pageSize int, category, sort string) (*ProductsResult, []Filter, []string, error) {
	// Call multiple services in parallel
	// Simplified implementation
	products := &ProductsResult{
		Items: []Product{},
		Total: 0,
	}
	filters := []Filter{}
	breadcrumb := []string{"Home", category}

	return products, filters, breadcrumb, nil
}

func (b *WebBFF) fetchProductDetails(ctx context.Context, id string) (*Product, *ReviewsResult, []Product, error) {
	// Fetch product, reviews, and related products in parallel
	product := &Product{}
	reviews := &ReviewsResult{}
	related := []Product{}

	return product, reviews, related, nil
}

func (b *WebBFF) getAvailability(stock int) string {
	if stock > 0 {
		return "In Stock"
	}
	return "Out of Stock"
}

// Start starts the BFF server.
func (b *WebBFF) Start() error {
	b.logger.Info("starting web BFF", "addr", b.server.Addr)
	return b.server.ListenAndServe()
}

func (b *WebBFF) writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (b *WebBFF) writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func getIntParam(r *http.Request, name string, defaultValue int) int {
	value := r.URL.Query().Get(name)
	if value == "" {
		return defaultValue
	}

	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}

	return intValue
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
```

---

### Mobile BFF

```go
package bff

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// MobileProductList represents the mobile-optimized product list response.
type MobileProductList struct {
	Products   []MobileProduct `json:"products"`
	NextCursor *string         `json:"nextCursor,omitempty"`
	HasMore    bool            `json:"hasMore"`
}

// MobileProduct represents a compact product for mobile.
type MobileProduct struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	Thumbnail string  `json:"thumbnail"`
	Rating    float64 `json:"rating"`
	InStock   bool    `json:"inStock"`
}

// MobileBFF implements the backend for frontend for mobile clients.
type MobileBFF struct {
	mux    *http.ServeMux
	server *http.Server
	logger *slog.Logger
}

// NewMobileBFF creates a new mobile BFF.
func NewMobileBFF(port int, logger *slog.Logger) *MobileBFF {
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	bff := &MobileBFF{
		mux:    mux,
		logger: logger,
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
		},
	}

	bff.setupRoutes()
	return bff
}

func (b *MobileBFF) setupRoutes() {
	// Product list optimized for mobile
	b.mux.HandleFunc("/products", b.handleProducts)

	// Compact product detail
	b.mux.HandleFunc("/products/", b.handleProductDetail)

	// Separate endpoint for lazy loading
	b.mux.HandleFunc("/products/{id}/reviews", b.handleProductReviews)
}

func (b *MobileBFF) handleProducts(w http.ResponseWriter, r *http.Request) {
	cursor := r.URL.Query().Get("cursor")
	limit := getIntParam(r, "limit", 20)
	category := r.URL.Query().Get("category")

	products, err := b.fetchProducts(r.Context(), cursor, limit, category)
	if err != nil {
		b.writeError(w, http.StatusInternalServerError, "failed to fetch products")
		return
	}

	mobileProducts := make([]MobileProduct, len(products.Items))
	for i, p := range products.Items {
		mobileProducts[i] = MobileProduct{
			ID:        p.ID,
			Name:      truncate(p.Name, 50), // Truncate for mobile
			Price:     p.Price,
			Thumbnail: b.getOptimizedImage(p.Images[0], 200), // Smaller image
			Rating:    p.Rating.Average,
			InStock:   p.Stock > 0,
		}
	}

	response := MobileProductList{
		Products:   mobileProducts,
		NextCursor: products.NextCursor,
		HasMore:    products.HasMore,
	}

	// Headers for mobile caching
	etag := b.generateETag(response)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("ETag", etag)

	b.writeJSON(w, response)
}

func (b *MobileBFF) handleProductDetail(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/products/"):]

	product, err := b.fetchProduct(r.Context(), id)
	if err != nil {
		b.writeError(w, http.StatusNotFound, "product not found")
		return
	}

	images := make([]string, 0, 3)
	for i := 0; i < 3 && i < len(product.Images); i++ {
		images = append(images, b.getOptimizedImage(product.Images[i], 400))
	}

	response := map[string]interface{}{
		"id":          product.ID,
		"name":        product.Name,
		"price":       product.Price,
		"images":      images,
		"description": truncate(product.Description, 300),
		"rating":      product.Rating.Average,
		"reviewCount": product.Rating.Count,
		"inStock":     product.Stock > 0,
		// No related products, reviews - lazy load
	}

	b.writeJSON(w, response)
}

func (b *MobileBFF) handleProductReviews(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/products/"):]
	id = id[:len(id)-len("/reviews")]

	reviews, err := b.fetchReviews(r.Context(), id, 5)
	if err != nil {
		b.writeError(w, http.StatusInternalServerError, "failed to fetch reviews")
		return
	}

	items := make([]map[string]interface{}, len(reviews.Items))
	for i, rev := range reviews.Items {
		items[i] = map[string]interface{}{
			"id":     rev.ID,
			"rating": rev.Rating,
			"text":   truncate(rev.Text, 200),
			"author": rev.Author,
			"date":   rev.CreatedAt,
		}
	}

	response := map[string]interface{}{
		"items":   items,
		"hasMore": len(reviews.Items) >= 5,
	}

	b.writeJSON(w, response)
}

func (b *MobileBFF) getOptimizedImage(url string, width int) string {
	// CDN image resizing
	return fmt.Sprintf("%s?w=%d&format=webp&quality=80", url, width)
}

func (b *MobileBFF) generateETag(data interface{}) string {
	jsonData, _ := json.Marshal(data)
	hash := md5.Sum(jsonData)
	return `"` + hex.EncodeToString(hash[:])[:20] + `"`
}

func (b *MobileBFF) fetchProducts(ctx context.Context, cursor string, limit int, category string) (*ProductsResult, error) {
	// Implementation simplified
	return &ProductsResult{
		Items:      []Product{},
		NextCursor: nil,
		HasMore:    false,
	}, nil
}

func (b *MobileBFF) fetchProduct(ctx context.Context, id string) (*Product, error) {
	return &Product{}, nil
}

func (b *MobileBFF) fetchReviews(ctx context.Context, productID string, limit int) (*ReviewsResult, error) {
	return &ReviewsResult{}, nil
}

// Start starts the mobile BFF server.
func (b *MobileBFF) Start() error {
	b.logger.Info("starting mobile BFF", "addr", b.server.Addr)
	return b.server.ListenAndServe()
}

func (b *MobileBFF) writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (b *MobileBFF) writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
```

---

### IoT BFF

```go
package bff

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// IoTProductData represents minimal product data for IoT devices.
type IoTProductData struct {
	I string `json:"i"` // id (abbreviated)
	P int    `json:"p"` // price in cents
	S int    `json:"s"` // stock (0 or 1)
}

// IoTBFF implements the backend for frontend for IoT devices.
type IoTBFF struct {
	mux    *http.ServeMux
	server *http.Server
	logger *slog.Logger
}

// NewIoTBFF creates a new IoT BFF.
func NewIoTBFF(port int, logger *slog.Logger) *IoTBFF {
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	bff := &IoTBFF{
		mux:    mux,
		logger: logger,
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
		},
	}

	bff.setupRoutes()
	return bff
}

func (b *IoTBFF) setupRoutes() {
	// Minimal data for constrained devices
	b.mux.HandleFunc("/p", b.handleProducts)

	// Price check only (for barcode scanners)
	b.mux.HandleFunc("/p/", b.handleProductPrice)
}

func (b *IoTBFF) handleProducts(w http.ResponseWriter, r *http.Request) {
	products, err := b.fetchProducts(r.Context(), 10)
	if err != nil {
		b.writeError(w, http.StatusInternalServerError, "failed to fetch products")
		return
	}

	response := make([]IoTProductData, len(products.Items))
	for i, p := range products.Items {
		stockFlag := 0
		if p.Stock > 0 {
			stockFlag = 1
		}

		response[i] = IoTProductData{
			I: p.ID,
			P: int(p.Price * 100), // Cents, integer
			S: stockFlag,
		}
	}

	// Binary-friendly response
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")

	json.NewEncoder(w).Encode(response)
}

func (b *IoTBFF) handleProductPrice(w http.ResponseWriter, r *http.Request) {
	sku := r.URL.Path[len("/p/"):]
	if len(sku) > 6 && sku[len(sku)-6:] == "/price" {
		sku = sku[:len(sku)-6]
	}

	product, err := b.fetchProductBySku(r.Context(), sku)
	if err != nil {
		b.writeError(w, http.StatusNotFound, "product not found")
		return
	}

	response := map[string]int{
		"p": int(product.Price * 100),
	}

	json.NewEncoder(w).Encode(response)
}

func (b *IoTBFF) fetchProducts(ctx context.Context, limit int) (*ProductsResult, error) {
	// Implementation simplified
	return &ProductsResult{Items: []Product{}}, nil
}

func (b *IoTBFF) fetchProductBySku(ctx context.Context, sku string) (*Product, error) {
	return &Product{}, nil
}

// Start starts the IoT BFF server.
func (b *IoTBFF) Start() error {
	b.logger.Info("starting IoT BFF", "addr", b.server.Addr)
	return b.server.ListenAndServe()
}

func (b *IoTBFF) writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
```

---

## GraphQL Federation as an Alternative

GraphQL offers an alternative to the BFF pattern by allowing clients to request exactly the data they need through a single endpoint. Each client (web, mobile) can perform different queries tailored to its specific needs.

---

## When to Use

- Multiple client types (web, mobile, desktop)
- Very different needs per platform
- Critical network optimization (mobile)
- Independent frontend teams

---

## When NOT to Use

- A single client type
- Simple RESTful API is sufficient
- Team too small to maintain multiple BFFs
- Clients with similar needs

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| [API Gateway](api-gateway.md) | BFF behind the gateway |
| GraphQL | Alternative with a single endpoint |
| [Sidecar](sidecar.md) | Shared functions between BFFs |
| CQRS | Read models per client |

---

## Sources

- [Sam Newman - BFF Pattern](https://samnewman.io/patterns/architectural/bff/)
- [Microsoft - BFF Pattern](https://learn.microsoft.com/en-us/azure/architecture/patterns/backends-for-frontends)
- [Netflix - BFF at Scale](https://netflixtechblog.com/optimizing-the-netflix-api-5c9ac715cf19)
