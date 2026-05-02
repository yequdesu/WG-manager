use crossterm::event::{self, Event as CrosstermEvent, KeyEvent, MouseEvent};
use std::sync::mpsc;
use std::time::Duration;

use crate::api;

#[derive(Debug)]
pub enum Event {
    Init,
    Quit,
    Error(String),
    Key(KeyEvent),
    Mouse(MouseEvent),
    Tick,
    DataReady,
}

#[derive(Debug)]
pub enum DataEvent {
    PeersFetched(Result<api::PeerListResponse, String>),
    RequestsFetched(Result<api::RequestListResponse, String>),
    StatusFetched(Result<api::ServerStatus, String>),
    RequestApproved(Result<bool, String>),
    RequestDenied(Result<bool, String>),
    PeerDeleted(Result<bool, String>),
}

pub struct EventHandler {
    tick_rate: Duration,
    data_rx: mpsc::Receiver<DataEvent>,
}

impl EventHandler {
    pub fn new(tick_rate_ms: u64, data_rx: mpsc::Receiver<DataEvent>) -> Self {
        Self {
            tick_rate: Duration::from_millis(tick_rate_ms),
            data_rx,
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

    pub fn try_recv_data(&self) -> Option<DataEvent> {
        self.data_rx.try_recv().ok()
    }
}
