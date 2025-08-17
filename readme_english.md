# Hexxagon Go

Hexxagon Go is a hexagonal board strategy game written in Go, faithfully implementing the core mechanics of the classic [Hexxagon](https://hexxagon.com/) game.

## 🎮 Game Overview

Hexxagon is a turn-based strategy game played on a hexagonal grid. Players take turns moving their pieces to occupy more tiles. The objective is to control the majority of the board when no more moves are possible or the board is full.

## 🧩 Board Configuration

Each cell on the hexagonal board can be in one of four states:

* **Empty**: A normal tile available for placement.
* **Disabled**: A tile that cannot be entered or occupied by any piece.
* **Player A Piece**: Occupied by Player A.
* **Player B Piece**: Occupied by Player B.

## 🕹️ Movement Rules

On your turn, you may move any one of your pieces using one of two move types:

1. **Copy Move**

   * Move a piece to an **adjacent** cell.
   * The original piece remains in place, and a new piece is created at the destination (i.e., clone/split).

2. **Jump Move**

   * Move a piece to a **non-adjacent but reachable** cell.
   * The original cell becomes empty, and the piece relocates (no cloning).

## ♻️ Conversion Mechanism

After a move, any opponent pieces **adjacent** to the destination cell will be **converted** to your pieces.

## 🏁 Victory Conditions

* The game ends when the board is full or no valid moves remain.
* The player with the most pieces on the board at the end wins.

## 🖥️ Launch Options

```bash
# Player vs AI
./hexxagon -mode=pve

# Player vs AI with move score tips (next 2 turns)
./hexxagon -mode=pve -tip=true

# Player vs Player
./hexxagon -mode=pvp
```
