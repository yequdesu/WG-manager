use std::env;
use std::fs;
use std::path::PathBuf;

#[derive(Debug, Clone)]
pub struct Config {
    pub api_url: String,
    pub api_key: String,
}

impl Config {
    pub fn load() -> Self {
        let path = Self::config_path();
        let mut api_url = String::from("http://127.0.0.1:58880");
        let mut api_key = String::new();

        if let Ok(content) = fs::read_to_string(&path) {
            for line in content.lines() {
                let line = line.trim();
                if line.starts_with('#') || line.is_empty() {
                    continue;
                }
                if let Some((key, value)) = line.split_once('=') {
                    let val = value.trim();
                    match key.trim() {
                        "MGMT_LISTEN" => {
                            let addr = val.replace("0.0.0.0", "127.0.0.1");
                            api_url = format!("http://{}", addr);
                        }
                        "MGMT_API_KEY" => {
                            api_key = val.to_string();
                        }
                        _ => {}
                    }
                }
            }
        }

        Self { api_url, api_key }
    }

    fn config_path() -> PathBuf {
        let project_dir = env::current_dir()
            .unwrap_or_else(|_| PathBuf::from("."));

        let local_config = project_dir.join("config.env");
        if local_config.exists() {
            return local_config;
        }

        if let Ok(home) = env::var("HOME") {
            let home_config = PathBuf::from(&home).join("WG-manager").join("config.env");
            if home_config.exists() {
                return home_config;
            }
        }

        local_config
    }
}
