// ============================================================
// AGENT: REPLACE — Replace with your API client.
//
// Pattern to keep:
// - ApiClient wraps reqwest::Client with base_url + api_key
// - Each API method: async fn, returns Result<YourType, String>
// - HTTP methods: self.client.get/post/delete + .json() deserialization
// - Authorization header pattern: Bearer token
//
// Replace: domain types, endpoint paths, method names.
// ============================================================

use serde::{Deserialize, Serialize};

// ============================================================
// AGENT: REPLACE — Define your API response types.
// ============================================================

/// Example: a generic API response.
/// Replace with your actual data structures.
#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct HealthResponse {
    pub status: String,
}

// ============================================================
// AGENT: KEEP — ApiClient pattern.
// Replace base_url, api_key sources, and add your own methods.
// ============================================================

#[derive(Debug, Clone)]
pub struct ApiClient {
    pub base_url: String,
    pub api_key: String,
    client: reqwest::Client,
}

impl ApiClient {
    pub fn new(base_url: String, api_key: String) -> Self {
        let client = reqwest::Client::builder()
            .timeout(std::time::Duration::from_secs(5))
            .build()
            .expect("failed to create HTTP client");
        Self { base_url, api_key, client }
    }

    // ============================================================
    // AGENT: ADD — Your API methods.
    // Follow this template for each endpoint:
    //
    // pub async fn your_method(&self) -> Result<YourType, String> {
    //     let url = format!("{}/api/v1/your-path", self.base_url);
    //     let mut req = self.client.get(&url);
    //     if !self.api_key.is_empty() {
    //         req = req.header("Authorization", format!("Bearer {}", self.api_key));
    //     }
    //     let resp = req.send().await.map_err(|e| e.to_string())?;
    //     resp.json::<YourType>().await.map_err(|e| e.to_string())
    // }
    // ============================================================

    /// Example: health check (replace with your first API call)
    pub async fn health_check(&self) -> Result<HealthResponse, String> {
        let url = format!("{}/api/v1/health", self.base_url);
        let resp = self.client
            .get(&url)
            .send()
            .await
            .map_err(|e| e.to_string())?;
        resp.json::<HealthResponse>().await.map_err(|e| e.to_string())
    }
}
