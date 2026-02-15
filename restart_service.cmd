@echo off
echo Restarting AskFlow service...
taskkill /F /IM helpdesk.exe 2>nul
timeout /t 2 /nobreak >nul
start "" helpdesk.exe
echo Service restarted!
pause
