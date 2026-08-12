package brand

import "time"

type Config struct {
	ProductName  string    `json:"product_name"`
	PrimaryColor string    `json:"primary_color"`
	Logo         []byte    `json:"logo"`
	LogoMIME     string    `json:"logo_mime"`
	Watermark    string    `json:"watermark"`
	UpdatedBy    string    `json:"updated_by"`
	UpdatedAt    time.Time `json:"updated_at"`
}
