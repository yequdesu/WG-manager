// ============================================================
// AGENT: REPLACE — Implement BackgroundEffect trait for your
// own background animation. Replace NoopBackground in app.rs.
// See examples/particles.rs for a physics-based reference.
// ============================================================

use ratatui::buffer::Buffer;
use ratatui::layout::Rect;

/// Trait for background animation effects.
/// Implement this trait to create custom background visuals.
pub trait BackgroundEffect {
    /// Called every frame. Update animation state here.
    fn update(&mut self, area: Rect, window: Rect, tick: u64);

    /// Render the effect to the buffer.
    fn render(&self, area: Rect, buf: &mut Buffer);
}

/// Default background: renders nothing (plain dark background).
pub struct NoopBackground;

impl BackgroundEffect for NoopBackground {
    fn update(&mut self, _area: Rect, _window: Rect, _tick: u64) {}
    fn render(&self, _area: Rect, _buf: &mut Buffer) {}
}
