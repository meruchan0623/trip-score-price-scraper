package main

import (
	"os"

	"trip-score-price-scraper/internal/tripcdp"
)

func main() {
	os.Exit(tripcdp.Run(os.Args[1:]))
}
