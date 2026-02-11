# Hexxagon Go (NN Edition)

Hexxagon Go is a high-performance implementation of the **Hexxagon** board game developed in Go. This project has transitioned from traditional static evaluation to a deep learning-based **Neural Network (NN)** engine, delivering a professional-level AI experience.

## 📜 Game Rules

Hexxagon is a strategic game played on a 61-cell hexagonal grid.

## 📺 Game Demonstration

![Hexxagon Demo](demo_github.mp4)

### 1. Board and Pieces
- The board consists of 61 hexagonal cells (radius 4).
- Two players compete using Red and White pieces.

### 2. Movement
On your turn, you can move one of your pieces in two ways:
- **Clone (Split)**: Move to an **adjacent (1-space distance)** empty cell. The original piece stays, and a new piece of your color is created at the destination.
- **Jump**: Move to an empty cell **2 spaces away**. The piece moves from the original location to the destination (the original cell becomes empty).

### 3. Infection (Capture) Mechanism
- When a piece is placed, all **adjacent (1-space distance)** opponent pieces are immediately converted to your color. This is the primary way to gain territory and flip the tide of the game.

### 4. Winning
- The game ends when the board is full or no legal moves remain for either player.
- The player with the most pieces on the board wins.

## 🧠 Neural Network AI Features

The project integrates an advanced neural network evaluation system:
- **Architecture**: Based on KataGo V7, supporting 22 spatial feature planes and 19 global features.
- **Training**: Trained using Reinforcement Learning through [KataGomo-Hexxagon](https://github.com/hzyhhzy/KataGomo/tree/Hexxagon).
- **Inference Optimization**: Powered by ONNX Runtime.
  - **Windows**: Supports **TensorRT (Hardware Acceleration)**, **CUDA**, and **DirectML (DirectX 12 fallback)**.
  - **macOS/Linux**: Supports CoreML (macOS) or optimized CUDA/CPU inference.
- **Hybrid Search**: Combines Alpha-Beta pruning with high-precision neural network scoring.

## 🚀 Quick Start

### Environment
- **Windows (Recommended)**: 
  - Acceleration Priority: **TensorRT > CUDA > DirectML**.
  - Essential libraries (`onnxruntime.dll`, `DirectML.dll`, etc.) are automatically extracted for a seamless experience.
- **macOS/Linux**: Automatically falls back to CoreML or CPU inference.

### Launch Commands
```powershell
# Human vs AI (Default depth 1; NN depth 1 or 2 is recommended)
./hexxagon.exe -depth 1

# Professional Analysis Mode (Displays policy percentages & real-time win probability)
./hexxagon.exe -depth 1 -tip

# Two-player Mode
./hexxagon.exe -mode pvp
```

## 📊 Professional UI Analysis (`-tip` flag)

When enabled, the UI provides real-time neural network insights:
- **Live Win Probability**: Displays the win percentage for both players in the top-left corner.
- **Move Policy Hints**: When a piece is selected, each legal destination shows its **Policy probability percentage**.
- **Heatmap Display**: Text brightness/color dynamically adjusts based on probability—higher chances appear stronger, helping you identify the best moves.

## 🧮 Performance Optimizations

- **Model Compression**: Supports `.onnx.gz` format to reduce executable size.
- **Batch Evaluation**: Employs Batch Inference to maximize GPU throughput during search.
- **Efficient Encoding**: Bitboard-driven move generation.

## 📜 License

- This project is free for learning, research, and non-commercial purposes. Please attribute the repository link and author information.
- **Commercial use is strictly prohibited**.
