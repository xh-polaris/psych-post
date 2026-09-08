package conf

type ModelConfig struct {
	Chat map[string]*ChatConfig
}

type ChatConfig struct {
	URL       string
	AccessKey string
	Model     string `json:",optional"`
}
