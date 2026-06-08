package main

import (
	"flag"
	"log"
	"time"

	"github.com/202121000995/OneSync/internal/certutil"
)

func main() {
	hosts := flag.String("hosts", "", "comma-separated DNS names or IP addresses for the certificate")
	certPath := flag.String("cert", "onesync.crt", "certificate output path")
	keyPath := flag.String("key", "onesync.key", "private key output path")
	validDays := flag.Int("days", 365, "certificate validity in days")
	flag.Parse()

	if err := certutil.Generate(certutil.Options{
		Hosts:    []string{*hosts},
		CertPath: *certPath,
		KeyPath:  *keyPath,
		Validity: time.Duration(*validDays) * 24 * time.Hour,
	}); err != nil {
		log.Fatal(err)
	}
	log.Printf("generated certificate %s and private key %s", *certPath, *keyPath)
}
