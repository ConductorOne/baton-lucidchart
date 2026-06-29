package main

import (
	cfg "github.com/conductorone/baton-lucidchart/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/config"
)

func main() {
	config.Generate("lucidchart", cfg.Config)
}
