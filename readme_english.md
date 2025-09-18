# Hexxagon Go

Hexxagon Go is a board game implementation of **Hexxagon** written in Go. It includes full game rules, a graphical user interface, sound effects, and multiple AI strategies.

## 🎮 Game Overview

The Hexxagon board is a hexagonal grid with a radius of 4. The goal is to occupy more cells than your opponent before the board is filled or one side has no legal moves left.

## 🧩 Board Elements

* **Empty Cell**: Normal position where pieces can be placed or moved through.
* **Obstacle**: Unusable cell; no piece can move into it.
* **Red/White Pieces**: Current positions of the two players.

## ➡️ Movement Rules

On a player's turn, they may choose one of their pieces and perform one of two moves:

1. **Clone Move**: Move to an adjacent cell (distance 1). The original piece stays, and a new piece is created in the destination.
2. **Jump Move**: Jump over one cell to a position at distance 2. The original cell becomes empty.

## 🔄 Infection Mechanism

After either a clone or jump move, all adjacent enemy pieces around the destination cell are immediately "infected" and converted to the moving player’s color.

## 🏁 Win Condition

* The game ends when the board is full or one side has no legal moves.
* The player with more pieces on the board wins.

## 🚀 Quick Start

```bash
# Human vs AI (default mode)
./hexxagon

# Human vs AI with score hints (depth 4)
./hexxagon --tip=true

# Two-player mode
./hexxagon --mode=pvp
```

## 🤖 AI Implementation

* Search uses iterative deepening with root-level parallelization, ordering legal moves by heuristic score.
* Static evaluation (`internal/game/evaluate.go`) considers piece count, edge control, triangle formations, and applies dynamic weighting based on game state.
* Each recursive layer applies Alpha-Beta pruning with a transposition table (`tt.go`) to cache results and reduce branching.
* Additional pruning rules include strategic filtering, risky jump avoidance, and infection count estimation to stabilize move selection.
* When the `-tip` flag is enabled, the UI runs a depth-4 search on candidate moves to provide real-time score hints to the player.

## 🧠 CNN Model Training

* Training data is generated from self-play matches.
* Training code is based on PyTorch with DDP and AMP, using a deep CNN on the 81-cell board with 6 rotation/mirror augmentations (12× expansion). Multi-GPU is supported.
* The trained model is saved as `hex_cnn.pt`, then converted via `export_hex_cnn_to_onnx.py` into `assets/hex_cnn.onnx` for in-game inference (`internal/ml/onnx_infer.go`).

## 🧮 Inference Backends

* **GPU Inference (Windows/CUDA)**: Place `onnxruntime_providers_cuda.dll`, `onnxruntime_providers_tensorrt.dll`, `cudnn*_9.dll`, etc. in the same directory as the executable, and set `ONNXRUNTIME_SHARED_LIBRARY_PATH` to point to the GPU version of `onnxruntime.dll`.
* **CPU Inference**: If CUDA dependencies are missing or on non-Windows OS, the program automatically falls back to CPU inference, extracting CPU runtime DLLs automatically with no additional setup.

## 📜 License

* Free to use for learning, research, and non-commercial purposes. Attribution to the repository link and author is required.
* Commercial use is strictly prohibited, including redistribution, paid distribution, or embedding in paid services.
