# self-clear 讓 AI 模型能從 claude code 內部自己執行 /clear 指令

在 claude code 內部執行的 AI 模型無法自己執行 /clear，必須由使用者親自下指令，本專案仿照 self-compact 的做法，對 claude code 注入指令來達成

請參考 D:/projects/self-compact 的做法，它連以管理員權限執行的 claude code 都能正確注入，因此我們也要採用它的做法

本專案共有兩個元件：self-clear.exe 和 self-clear 這個 skill

安裝方式與子目錄結構稍微與 self-compact 不同。我們的子目錄結構必須符合安裝 self-clear.exe 時是以 go install {從 github 安裝}，而 self-clear 這個 skill 是以 npx skill add {從 github 安裝}

## 使用方法

以下使用方法是寫給施展的 AI 模型看的，因此應該也要整理到 SKILL.md。實作 self-clear.exe 也是請依照以下描述，將功能實作出來

由於施展 /self-clear 後，模型所在的 session 會被終止，系統產生新的 session 以後就 idle 了，因此注入 /clear，停頓特定秒數後，要注入一個新的提示詞來繼續。此外新的 session 中，上下文會被清空，因此 AI 模型必須先做好交接。施展此技能的步驟為：

1. 因上下文即將清空，請先寫交接事項至 project memory 或特定的 .md 檔中
2. 施展 /self-clear {要在新的 session 注入的提示詞}

範例如

1. 將交接事項寫入 next-session.md
2. 施展「/self-clear 請依照 next-session.md 內容執行」

考慮 /self-clear 後面如果 AI 模型不給提示詞，應該仍屬於可執行的用法。我們預設會注入 continue，並假設模型已經交接好讓「新 session 只看到 continue 仍能執行得下去」，例如將提示詞寫在 CLAUDE.md 或 AGENTS.md 或 project memory 中

SKILL.md 中要特別強調，如果目前 session 有 spawn 或 subagent 或呼叫 shell 等子程序，必須考慮是否等它們完成或 kill 掉再實施，若不 kill 掉必須交接給新的 session 繼續監控

# 驗收重點

撰寫完成後先不要 commit，等我驗收。我會執行以下步驟

1. 檢查 README.md 是否有說明安裝步驟
2. 先以 go install 及 npx skills add 做本地安裝，驗收通過以後再測試從 GitHub 安裝
3. 啟動 claude code 測試以下三種任務是否能正確執行

## 一般使用方式

```
現在要測試你是否能正確施展 /self-clear 技能。請依以下步驟重複進行三輪
1. 顯示目前 session ID
2. 暫停 2 秒後施展 /self-clear 清空上下文
3. 回到 1. 進行下一輪，若已進行三輪則停止並待命
```

## 僅使用預設提示詞 continue 的使用方式

```
現在要測試你是否能正確施展 /self-clear 技能，不帶參數提示詞。請將以下步驟重複進行三輪
1. 顯示目前 session ID
2. 暫停 2 秒後施展 /self-clear 清空上下文。注意！施展 /self-clear 時不帶任何參數提示詞
3. 回到 1. 進行下一輪，若已進行三輪則停止並待命
```

## 有呼叫 shell 子程序是否仍能正確交接

```
現在要測試你呼叫 shell 後，是否仍能正確施展 /self-clear 技能，交接給新的 session。請執行以下步驟
1. 請先呼叫一個 shell，執行程式：先暫停 3 分鐘後，返回一個隨機的整數。此 shell 必須於背景執行，讓你能馬上回來繼續往下進行
以下 2. 到 4. 重複進行三輪
2. 顯示目前 session ID
3. 暫停 2 秒後施展 /self-clear 清空上下文
4. 回到 2. 進行下一輪，若已進行三輪則往下執行 5.
5. 等待 1. 呼叫的 shell，取得其返回的隨機整數，於你的 turn 回覆結果為何
```

## 最後驗收從 GitHub 安裝

通過上述驗收後，我會請你 commit 並用 gh 將本專案推送成為 public repository。接下來我會測試是否能從 GitHub 安裝

0. 先移除先前測試時安裝的版本
1. 依 README.md 說明，以 go install {從 github 安裝} 安裝 self-clear.exe
2. 依 README.md 說明，以 npx skills add {從 github 安裝} 安裝 self-clear 這個 skill
3. 啟動 claude code 再測試一次「一般使用方式」是否能正確執行

# 專案要求

程式及文件還有 commit message 等，請用英文撰寫

專案中某處必須標示開發者 AI 模型。你是 Opus 5
