# Hexxagon Go (NN Edition)

Hexxagon Go is a high-performance implementation of the **Hexxagon** board game developed in Go. This project has transitioned from traditional static evaluation to a deep learning-based **Neural Network (NN)** engine, delivering a professional-level AI experience.

## 🎮 Game Overview

Hexxagon is a strategic game played on a 61-cell (radius 4) hexagonal grid. Players move pieces via "cloning" or "jumping" to expand territory and infect opponent pieces. The goal is to control the majority of the board when no legal moves remain.

## 🧠 Neural Network AI Features

The project integrates an advanced neural network evaluation system:
- **Architecture**: Based on KataGo V7, supporting 22 spatial feature planes and 19 global features.
- **Training**: Trained using Reinforcement Learning through [KataGomo-Hexxagon](https://github.com/hzyhhzy/KataGomo/tree/Hexxagon).
- **Inference Optimization**: Powered by ONNX Runtime with **CUDA GPU acceleration**. It utilizes **Batch Inference** technology to evaluate dozens of moves simultaneously, drastically increasing search efficiency.
- **Hybrid Search**: Combines Alpha-Beta pruning with high-precision neural network scoring to achieve superhuman playing strength.

## 🚀 Quick Start

### Environment
- **Windows (Recommended)**: Supports CUDA acceleration out of the box (requires proper drivers and DLLs).
- **macOS/Linux**: Automatically falls back to optimized CPU inference.

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
- **Heatmap Display**: Text brightness dynamically adjusts based on probability—higher chances appear brighter/stronger, helping you identify the best tactical moves instantly.

## 🧮 Performance Optimizations

- **Model Compression**: Supports `.onnx.gz` format to reduce executable size while maintaining fast load times.
- **Batch Evaluation**: Employs Batch Inference at the root and shallow tree levels to maximize GPU throughput.
- **Efficient Encoding**: Bitboard-driven move generation paired with precise neural network intuition.

## 📜 License

- This project is free for learning, research, and non-commercial purposes. Please attribute the repository link and author information when used.
- **Commercial use is strictly prohibited** (including but not limited to redistribution for profit, paid distribution, or embedding in paid services).