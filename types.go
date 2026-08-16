package main

type Settings struct {
	ProxyIP   string `yaml:"proxy_ip,omitempty"`
	DNSDomain string `yaml:"dns_domain,omitempty"`
}

type Domain struct {
	Type string `yaml:"type"`
	Port int    `yaml:"port,omitempty"`
	IP   string `yaml:"ip,omitempty"`
}

type Config struct {
	Settings Settings          `yaml:"settings"`
	Domains  map[string]Domain `yaml:"domains"`
	GeoSites []string          `yaml:"geosites,omitempty"`
}

type Stack struct {
	Angie  bool
	Blocky bool
}

type Paths struct {
	ConfigDir     string
	DataDir       string
	BackupDir     string
	ConfigFile    string
	AppliedConfig string
	Blocky        string
	AngieMain     string
	AngieStream   string
	AngieHTTP     string
	BlockyList    string
	BlockyCertSum string
	DLC           string
	DLCSum        string
}
