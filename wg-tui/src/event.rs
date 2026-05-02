use crossterm::event::{self, Event as CrosstermEvent, KeyEvent, MouseEvent};
use std::time::Duration;

#[allow(dead_code)]
#[derive(Debug)]
pub enum Event {
    Init,
    Quit,
    Error(String),
    Key(KeyEvent),
    Mouse(MouseEvent),
    Tick,
    DataRefresh,
}

pub struct EventHandler {
    tick_rate: Duration,
    refresh_rate: Duration,
}

impl EventHandler {
    pub fn new(tick_rate_ms: u64, refresh_rate_ms: u64) -> Self {
        Self {
            tick_rate: Duration::from_millis(tick_rate_ms),
            refresh_rate: Duration::from_millis(refresh_rate_ms),
        }
    }

    pub fn next(&self) -> Result<Event, Box<dyn std::error::Error>> {
        if event::poll(self.tick_rate)? {
            match event::read()? {
                CrosstermEvent::Key(key) => Ok(Event::Key(key)),
                CrosstermEvent::Mouse(mouse) => Ok(Event::Mouse(mouse)),
                CrosstermEvent::Resize(_, _) => Ok(Event::Init),
                _ => self.next(),
            }
        } else {
            Ok(Event::Tick)
        }
    }
}
