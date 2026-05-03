// ============================================================
// AGENT: REPLACE — Replace with your configuration loader.
//
// Pattern to keep:
// - load() reads a key=value config file
// - Falls back to environment variables or hardcoded defaults
// - Parses simple types: String, integer, boolean
//
// Replace: config keys, default values, file path.
// ============================================================

use std::env;
use std::fs;
use std::path::PathBuf;

// ============================================================
// AGENT: REPLACE — Your config fields.
// ============================================================

#[derive(Debug, Clone)]
pub struct Config {
    pub api_url: String,
    pub api_key: String,
    // AGENT: ADD — Your config fields here
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

                    // ============================================================
                    // AGENT: REPLACE — Parse your config keys.
                    // ============================================================
                    match key.trim() {
                        "API_URL" => api_url = val.to_string(),
                        "API_KEY" => api_key = val.to_string(),
                        // AGENT: ADD — More config keys
                        _ => {}
                    }
                }
            }
        }

        Self { api_url, api_key }
    }

    fn config_path() -> PathBuf {
        // AGENT: REPLACE — Change the config file path
        let local_config = PathBuf::from("config.env");
        if local_config.exists() {
            return local_config;
        }

        if let Ok(home) = env::var("HOME") {
            let home_config = PathBuf::from(&home).join(".config").join("your-app").join("config.env");
            if home_config.exists() {
                return home_config;
            }
        }

        local_config
    }
}
