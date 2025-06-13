package utils

import (
	"fmt"

	"github.com/jinzhu/configor"
)

var Config = struct {
	RabbitMQ struct {
		Host         string `yaml:"host"`
		Port         int    `yaml:"port"`
		User         string `yaml:"user"`
		Pass         string `yaml:"pass"`
		ExchangeName string `yaml:"exchange_name" default:"mediawiki.jobs"`
	} `yaml:"rabbitmq"`
	MediaWiki struct {
		JobPath string
	} `yaml:"mediawiki"`
	Queues struct {
		HighPriority   []string `yaml:"high_priority"`
		LowPriority    []string `yaml:"low_priority"`
		NormalPriority []string `yaml:"normal_priority"`
		Ignored        []string `yaml:"ignored"`
	} `yaml:"queues"`
	Workers struct {
		HighPriority   int `yaml:"high_priority" default:"3"`
		NormalPriority int `yaml:"normal_priority" default:"2"`
		LowPriority    int `yaml:"low_priority" default:"1"`
		PerQueue       int `yaml:"per_queue"`
	} `yaml:"workers"`
}{}

func InitConfiguration() {
	configor.Load(&Config, "config.yml")
	fmt.Println("Configuration loaded from config.yml")
}
