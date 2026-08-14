package brand

import "encoding/base64"

// PublicConfig is the login-page-safe projection of the governed brand config.
type PublicConfig struct {
	ProductName  string `json:"product_name"`
	PrimaryColor string `json:"primary_color"`
	LogoDataURL  string `json:"logo_data_url"`
}

func publicConfig(config Config) PublicConfig {
	logoDataURL := ""
	if len(config.Logo) > 0 && (config.LogoMIME == "image/png" || config.LogoMIME == "image/jpeg") {
		logoDataURL = config.LogoMIME + ";base64," + base64.StdEncoding.EncodeToString(config.Logo)
	}
	return PublicConfig{
		ProductName:  config.ProductName,
		PrimaryColor: config.PrimaryColor,
		LogoDataURL:  logoDataURL,
	}
}
