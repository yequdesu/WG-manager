use ratatui::layout::Rect;
use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WindowState {
    pub x: i16,
    pub y: i16,
    pub scale: u8,
    #[serde(skip)]
    prev_w: u16,
    #[serde(skip)]
    prev_h: u16,
}

impl WindowState {
    pub fn new() -> Self {
        Self {
            x: 0,
            y: 0,
            scale: 95,
            prev_w: 0,
            prev_h: 0,
        }
    }

    pub fn compute(&mut self, term: Rect) -> Rect {
        let w = (term.width as u32 * self.scale as u32 / 100) as u16;
        let h = (term.height as u32 * self.scale as u32 / 100) as u16;

        let w = w.max(44);
        let h = h.max(14);
        let w = w.min(term.width.saturating_sub(2));
        let h = h.min(term.height.saturating_sub(2));

        if self.prev_w == 0 {
            self.x = ((term.width.saturating_sub(w)) / 2) as i16;
            self.y = ((term.height.saturating_sub(h)) / 2) as i16;
            self.prev_w = term.width;
            self.prev_h = term.height;
            return Rect::new(self.x as u16, self.y as u16, w, h);
        }

        if term.width != self.prev_w || term.height != self.prev_h {
            let max_x = (term.width.saturating_sub(w)) as i16;
            let max_y = (term.height.saturating_sub(h)) as i16;
            self.x = self.x.clamp(0, max_x);
            self.y = self.y.clamp(0, max_y);
            self.prev_w = term.width;
            self.prev_h = term.height;
        }

        Rect::new(self.x as u16, self.y as u16, w, h)
    }

    pub fn move_by(&mut self, dx: i16, dy: i16) {
        self.x = (self.x + dx).max(0);
        self.y = (self.y + dy).max(0);
    }

    pub fn zoom_in(&mut self) {
        self.scale = (self.scale + 5).min(100);
        self.prev_w = 0;
        self.prev_h = 0;
    }

    pub fn zoom_out(&mut self) {
        self.scale = (self.scale.saturating_sub(5)).max(50);
        self.prev_w = 0;
        self.prev_h = 0;
    }

    pub fn reset(&mut self) {
        self.scale = 95;
        self.prev_w = 0;
        self.prev_h = 0;
    }

    pub fn save(&self) {
        if let Ok(json) = serde_json::to_string_pretty(self) {
            let path = state_path();
            if let Some(parent) = path.parent() {
                let _ = std::fs::create_dir_all(parent);
            }
            let _ = std::fs::write(&path, json);
        }
    }

    pub fn load() -> Self {
        let path = state_path();
        if let Ok(data) = std::fs::read_to_string(&path) {
            if let Ok(state) = serde_json::from_str(&data) {
                return state;
            }
        }
        Self::new()
    }
}

fn state_path() -> PathBuf {
    let base = if cfg!(target_os = "linux") || cfg!(target_os = "macos") {
        std::env::var("XDG_CONFIG_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|_| {
                let home = std::env::var("HOME").unwrap_or_else(|_| "/tmp".into());
                PathBuf::from(home).join(".config")
            })
    } else {
        std::env::var("APPDATA")
            .map(PathBuf::from)
            .unwrap_or_else(|_| PathBuf::from("."))
    };
    base.join("wg-tui").join("window-state.json")
}
