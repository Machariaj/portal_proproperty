@echo off
REM Schedule the activity monitor to run every 5 minutes
echo Setting up scheduled task for activity monitor...

schtasks /create /tn "ProProperty Activity Monitor" /tr "%~dp0run_monitor.bat" /sc minute /mo 5 /f

echo Scheduled task created. The monitor will run every 5 minutes.
echo You can view/edit the task in Task Scheduler.
pause