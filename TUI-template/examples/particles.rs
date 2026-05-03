// ============================================================
// EXAMPLE: Physics-based particle system background effect.
//
// Copy this file into your project's src/ directory and
// implement BackgroundEffect for ParticleSystem.
// Then in app.rs, replace Box::new(NoopBackground) with
// Box::new(ParticleSystem::new()).
//
// Features:
// - 60 particles with circular ASCII chars (● ○ ◎ ◉ ·)
// - Friction physics (0.985/frame), edge bounce, window repulsion
// - Large asteroids fly in from edges, scatter particles
// - Auto-spawns new particles when count drops below minimum
// ============================================================

use ratatui::buffer::Buffer;
use ratatui::layout::Rect;
use ratatui::style::Color;
use ratatui::widgets::Widget;
use rand::Rng;

const MAX_PARTICLES: usize = 80;
const MIN_PARTICLES: usize = 40;
const FRICTION: f32 = 0.985;
const STOP_THRESHOLD: f32 = 0.05;
const P_CHARS: &[char] = &['●', '○', '◎', '◉', '·'];

struct Particle {
    x: f32, y: f32, vx: f32, vy: f32,
    ch: char, base_alpha: f32,
}

pub struct ParticleSystem {
    particles: Vec<Particle>,
    rng: rand::rngs::StdRng,
    spawn_cooldown: u16,
}

impl ParticleSystem {
    pub fn new() -> Self {
        use rand::SeedableRng;
        Self { particles: Vec::new(), rng: rand::rngs::StdRng::from_os_rng(), spawn_cooldown: 0 }
    }

    pub fn update(&mut self, area: Rect, window: Rect, _tick: u64) {
        let w = area.width as f32;
        let h = area.height as f32;

        for p in &mut self.particles {
            p.vx *= FRICTION; p.vy *= FRICTION;
            p.x += p.vx; p.y += p.vy;
            if p.x < 0.0 { p.x = 0.0; p.vx = -p.vx * 0.5; }
            if p.x >= w - 0.01 { p.x = w - 0.01; p.vx = -p.vx * 0.5; }
            if p.y < 0.0 { p.y = 0.0; p.vy = -p.vy * 0.5; }
            if p.y >= h - 0.01 { p.y = h - 0.01; p.vy = -p.vy * 0.5; }
            if p.vx.abs() < STOP_THRESHOLD && p.vy.abs() < STOP_THRESHOLD { p.vx = 0.0; p.vy = 0.0; }
        }

        if self.particles.len() < MIN_PARTICLES {
            if self.spawn_cooldown == 0 {
                self.particles.push(Particle {
                    x: self.rng.random_range(0.0..w), y: -1.0,
                    vx: 0.0, vy: self.rng.random_range(0.2..0.6),
                    ch: P_CHARS[self.rng.random_range(0..P_CHARS.len())],
                    base_alpha: self.rng.random_range(0.15..0.50),
                });
                self.spawn_cooldown = 4;
            } else { self.spawn_cooldown = self.spawn_cooldown.saturating_sub(1); }
        }
        if self.particles.len() > MAX_PARTICLES {
            self.particles.drain(0..self.particles.len() - MAX_PARTICLES);
        }
    }
}

impl Widget for &ParticleSystem {
    fn render(self, area: Rect, buf: &mut Buffer) {
        for p in &self.particles {
            let x = p.x as u16; let y = p.y as u16;
            if x >= area.width || y >= area.height { continue; }
            if let Some(cell) = buf.cell_mut((area.x + x, area.y + y)) {
                cell.set_char(p.ch);
                let a = (p.base_alpha * 0.55).clamp(0.08, 0.55);
                cell.set_fg(Color::Rgb((210.0 * a) as u8, (224.0 * a) as u8, (255.0 * a) as u8));
            }
        }
    }
}

impl crate::background::BackgroundEffect for ParticleSystem {
    fn update(&mut self, area: Rect, window: Rect, tick: u64) {
        self.update(area, window, tick);
    }
}
