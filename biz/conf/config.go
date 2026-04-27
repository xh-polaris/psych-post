package conf

import (
	"os"

	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/core/stores/cache"

	"github.com/zeromicro/go-zero/core/conf"
)

var config *Config

type (
	RabbitMQ struct {
		Url   string
		Queue string
	}

	MongoDB struct {
		URL string
		DB  string
	}

	Cache struct {
		Addr     string
		Password string
		DB       int `json:"DB,omitempty,default=0"`
	}

	Config struct {
		service.ServiceConf
		ListenOn    string
		State       string
		CacheConf   cache.CacheConf
		Cache       *Cache
		RabbitMQ    *RabbitMQ
		Mongo       *MongoDB
		ModelConfig *ModelConfig
		Consumers   int
	}
)

func NewConfig() (*Config, error) {
	c := new(Config)
	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		path = "etc/config.yaml"
	}
	err := conf.Load(path, c)
	if err != nil {
		return nil, err
	}
	err = c.SetUp()
	if err != nil {
		return nil, err
	}
	config = c
	return c, nil
}

func GetConfig() *Config {
	if config == nil {
		_, _ = NewConfig()
	}
	return config
}
