#![allow(dead_code)]

use ratatui::buffer::Buffer;
use ratatui::layout::Rect;
use ratatui::style::Color;
use ratatui::widgets::Widget;
use rand::Rng;

const MAX_PARTICLES: usize = 120;
const MIN_PARTICLES: usize = 60;
const MAX_ASTEROIDS: usize = 3;
const FRICTION: f32 = 0.985;
const STOP_THRESHOLD: f32 = 0.05;
const ASTEROID_CHANCE: u32 = 380;
const P_CHARS: &[char] = &['●', '○', '◎', '◉', '·'];

struct Particle {
    x: f32,
    y: f32,
    vx: f32,
    vy: f32,
    ch: char,
    base_alpha: f32,
}

struct Asteroid {
    x: f32,
    y: f32,
    vx: f32,
    vy: f32,
    radius: usize,
    cells: Vec<Vec<Option<char>>>,
}

pub struct ParticleSystem {
    particles: Vec<Particle>,
    asteroids: Vec<Asteroid>,
    prev_window: Rect,
    rng: rand::rngs::StdRng,
    spawn_cooldown: u16,
    tick: u64,
}

impl ParticleSystem {
    pub fn new() -> Self {
        use rand::SeedableRng;
        Self {
            particles: Vec::with_capacity(MAX_PARTICLES),
            asteroids: Vec::with_capacity(MAX_ASTEROIDS),
            prev_window: Rect::default(),
            rng: rand::rngs::StdRng::from_os_rng(),
            spawn_cooldown: 0,
            tick: 0,
        }
    }

    pub fn update(&mut self, area: Rect, window: Rect, tick: u64) {
        self.tick = tick;

        let dx = window.x as i16 - self.prev_window.x as i16;
        let dy = window.y as i16 - self.prev_window.y as i16;
        let scaled_up = window.width > self.prev_window.width
            || window.height > self.prev_window.height;

        // ── Window move collision ──
        if dx != 0 || dy != 0 {
            self.push_by_window(window, dx as f32 * 0.8, dy as f32 * 0.8);
        }

        // ── Window scale-up collision ──
        if scaled_up {
            self.push_by_window(window, 0.0, 0.0);
        }

        // ── Particle physics ──
        let win_l = window.x as f32;
        let win_r = (window.x + window.width) as f32;
        let win_t = window.y as f32;
        let win_b = (window.y + window.height) as f32;

        for p in &mut self.particles {
            p.vx *= FRICTION;
            p.vy *= FRICTION;
            p.x += p.vx;
            p.y += p.vy;

            // ── Window edge bounce ──
            if p.x >= win_l && p.x < win_r && p.y >= win_t && p.y < win_b {
                let dl = (p.x - win_l).abs();
                let dr = (win_r - p.x).abs();
                let dt = (p.y - win_t).abs();
                let db = (win_b - p.y).abs();
                let min = dl.min(dr).min(dt).min(db);

                if min == dl {
                    p.x = win_l - 0.5;
                    p.vx = -p.vx * 0.6;
                } else if min == dr {
                    p.x = win_r + 0.5;
                    p.vx = -p.vx * 0.6;
                } else if min == dt {
                    p.y = win_t - 0.5;
                    p.vy = -p.vy * 0.6;
                } else {
                    p.y = win_b + 0.5;
                    p.vy = -p.vy * 0.6;
                }
            }

            // ── Screen edge bounce ──
            if p.x < 0.0 {
                p.vx = -p.vx * 0.5;
            }
            if p.x >= area.width as f32 - 0.01 {
                p.x = area.width as f32 - 0.01;
                p.vx = -p.vx * 0.5;
            }
            if p.y < 0.0 {
                p.y = 0.0;
                p.vy = -p.vy * 0.5;
            }
            if p.y >= area.height as f32 - 0.01 {
                p.y = area.height as f32 - 0.01;
                p.vy = -p.vy * 0.5;
            }

            if p.vx.abs() < STOP_THRESHOLD && p.vy.abs() < STOP_THRESHOLD {
                p.vx = 0.0;
                p.vy = 0.0;
            }
        }

        // ── Asteroid spawn ──
        if self.asteroids.len() < MAX_ASTEROIDS
            && self.rng.random_range(0..ASTEROID_CHANCE) == 0
        {
            self.spawn_asteroid(area);
        }

        // ── Asteroid move + collision ──
        for a in &mut self.asteroids {
            a.x += a.vx;
            a.y += a.vy;
        }
        if !self.asteroids.is_empty() {
            self.asteroid_particle_collision();
        }

        // ── Clean asteroids ──
        let (aw, ah) = (area.width as f32, area.height as f32);
        self.asteroids.retain(|a| {
            let r = a.radius as f32;
            a.x > -r * 3.0 && a.x < aw + r * 3.0 && a.y > -r * 3.0 && a.y < ah + r * 3.0
        });

        // ── Spawn particles ──
        let window_full = window.width as f32 > area.width as f32 * 0.95
            && window.height as f32 > area.height as f32 * 0.95;

        if !window_full && self.particles.len() < MIN_PARTICLES {
            if self.spawn_cooldown == 0 {
                self.spawn_particle(area);
                self.spawn_cooldown = 4;
            } else {
                self.spawn_cooldown = self.spawn_cooldown.saturating_sub(1);
            }
        }

        if self.particles.len() > MAX_PARTICLES {
            let excess = self.particles.len() - MAX_PARTICLES;
            self.particles.drain(0..excess);
        }

        self.prev_window = window;
    }

    fn push_by_window(&mut self, window: Rect, impulse_x: f32, impulse_y: f32) {
        let left = window.x as f32;
        let right = (window.x + window.width) as f32;
        let top = window.y as f32;
        let bot = (window.y + window.height) as f32;

        for p in &mut self.particles {
            if p.x >= left && p.x < right && p.y >= top && p.y < bot {
                let dl = (p.x - left).abs();
                let dr = (right - p.x).abs();
                let dt = (p.y - top).abs();
                let db = (bot - p.y).abs();
                let min = dl.min(dr).min(dt).min(db);

                if min == dl {
                    p.x = left - 0.6;
                    p.vx += impulse_x - 0.4;
                } else if min == dr {
                    p.x = right + 0.6;
                    p.vx += impulse_x + 0.4;
                } else if min == dt {
                    p.y = top - 0.6;
                    p.vy += impulse_y - 0.4;
                } else {
                    p.y = bot + 0.6;
                    p.vy += impulse_y + 0.4;
                }
            }
        }
    }

    fn spawn_particle(&mut self, area: Rect) {
        let edge: u8 = self.rng.random_range(0..4);
        let (x, y, vx, vy) = match edge {
            0 => (self.rng.random_range(0.0..area.width as f32), -1.0, 0.0, self.rng.random_range(0.2..0.6)),
            1 => (self.rng.random_range(0.0..area.width as f32), area.height as f32 + 1.0, 0.0, self.rng.random_range(-0.6..-0.2)),
            2 => (-1.0, self.rng.random_range(0.0..area.height as f32), self.rng.random_range(0.2..0.6), 0.0),
            _ => (area.width as f32 + 1.0, self.rng.random_range(0.0..area.height as f32), self.rng.random_range(-0.6..-0.2), 0.0),
        };

        self.particles.push(Particle {
            x,
            y,
            vx,
            vy,
            ch: P_CHARS[self.rng.random_range(0..P_CHARS.len())],
            base_alpha: self.rng.random_range(0.25..0.55),
        });
    }

    fn spawn_asteroid(&mut self, area: Rect) {
        let radius = self.rng.random_range(4..11usize);
        let edge: u8 = self.rng.random_range(0..4);
        let speed = self.rng.random_range(0.8..2.0);
        let (ax, ay, avx, avy): (f32, f32, f32, f32) = match edge {
            0 => (self.rng.random_range(0.0..area.width as f32), -(radius as f32) * 2.0, 0.0, speed),
            1 => (self.rng.random_range(0.0..area.width as f32), area.height as f32 + (radius as f32) * 2.0, 0.0, -speed),
            2 => (-(radius as f32) * 2.0, self.rng.random_range(0.0..area.height as f32), speed, 0.0),
            _ => (area.width as f32 + (radius as f32) * 2.0, self.rng.random_range(0.0..area.height as f32), -speed, 0.0),
        };

        let cells = Self::build_asteroid_cells(radius);
        self.asteroids.push(Asteroid { x: ax, y: ay, vx: avx, vy: avy, radius, cells });
    }

    fn build_asteroid_cells(radius: usize) -> Vec<Vec<Option<char>>> {
        let size = radius * 2 + 1;
        let mut grid = vec![vec![None; size]; size];
        let r = radius as f32;
        let center = radius as f32;

        for dy in 0..size {
            for dx in 0..size {
                let dist = ((dx as f32 - center).powi(2) + ((dy as f32 - center) * 2.0).powi(2)).sqrt();
                if dist <= r + 0.3 {
                    let ch = if dist <= r * 0.35 {
                        '█'
                    } else if dist <= r * 0.65 {
                        '▓'
                    } else if dist <= r * 0.85 {
                        '▒'
                    } else {
                        '░'
                    };
                    grid[dy][dx] = Some(ch);
                }
            }
        }
        grid
    }

    fn asteroid_particle_collision(&mut self) {
        for a in &self.asteroids {
            let ar = a.radius as f32;
            for p in &mut self.particles {
                let dx = p.x - a.x;
                let dy = p.y - a.y;
                let dist = (dx * dx + dy * dy).sqrt();
                let push_dist = ar + 2.5;
                if dist < push_dist && dist > 0.1 {
                    let force = (push_dist - dist) * 2.5;
                    let nx = dx / dist;
                    let ny = dy / dist;
                    p.vx += nx * force;
                    p.vy += ny * force;
                    p.x = a.x + nx * (push_dist + 0.5);
                    p.y = a.y + ny * (push_dist + 0.5);
                }
            }
        }
    }
}

impl Widget for &ParticleSystem {
    fn render(self, area: Rect, buf: &mut Buffer) {
        // Layer 1: stationary particles
        for p in &self.particles {
            if p.vx.abs() < 0.05 && p.vy.abs() < 0.05 {
                render_particle(buf, area, p);
            }
        }

        // Layer 2: moving particles
        for p in &self.particles {
            if !(p.vx.abs() < 0.05 && p.vy.abs() < 0.05) {
                render_particle(buf, area, p);
            }
        }

        // Layer 3: asteroids
        for a in &self.asteroids {
            render_asteroid(buf, area, a);
        }
    }
}

fn render_particle(buf: &mut Buffer, area: Rect, p: &Particle) {
    let x = p.x as u16;
    let y = p.y as u16;
    if x >= area.width || y >= area.height {
        return;
    }
    if let Some(cell) = buf.cell_mut((area.x + x, area.y + y)) {
        cell.set_char(p.ch);
        cell.set_fg(particle_color(p.base_alpha));
    }
}

fn render_asteroid(buf: &mut Buffer, area: Rect, a: &Asteroid) {
    let size = a.radius * 2 + 1;
    let ox = a.x as i32 - a.radius as i32;
    let oy = a.y as i32 - a.radius as i32;

    for dy in 0..size {
        for dx in 0..size {
            if let Some(ch) = a.cells[dy][dx] {
                let gx = ox + dx as i32;
                let gy = oy + dy as i32;
                if gx < 0 || gy < 0 || gx >= area.width as i32 || gy >= area.height as i32 {
                    continue;
                }
                let dist = ((dx as f32 - a.radius as f32).powi(2)
                    + ((dy as f32 - a.radius as f32) * 2.0).powi(2))
                .sqrt();
                let alpha = (1.0 - dist / a.radius as f32).clamp(0.08, 0.28);
                if let Some(cell) = buf.cell_mut((area.x + gx as u16, area.y + gy as u16)) {
                    cell.set_char(ch);
                    cell.set_fg(asteroid_color(alpha));
                }
            }
        }
    }
}

fn particle_color(alpha: f32) -> Color {
    let a = (alpha * 0.55).clamp(0.10, 0.55);
    Color::Rgb(
        (210.0 * a) as u8,
        (224.0 * a) as u8,
        (255.0 * a) as u8,
    )
}

fn asteroid_color(alpha: f32) -> Color {
    let a = alpha.clamp(0.10, 0.35);
    Color::Rgb(
        (180.0 * a) as u8,
        (200.0 * a) as u8,
        (255.0 * a) as u8,
    )
}
