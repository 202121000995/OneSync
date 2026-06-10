#pragma once

#include "SyncLink.h"

#include <QMap>
#include <QMainWindow>
#include <QStringList>

class QAction;
class QCloseEvent;
class QLabel;
class QMenu;
class QPushButton;
class QPlainTextEdit;
class QSystemTrayIcon;
class QTableWidget;
class QThread;

class MainWindow : public QMainWindow
{
    Q_OBJECT

public:
    explicit MainWindow(QWidget* parent = nullptr);

private slots:
    void addTask();
    void startSelectedTask();
    void pauseSelectedTask();
    void rescanSelectedTask();
    void editSelectedTask();
    void deleteSelectedTask();
    void exportDiagnostics();
    void showFromTray();
    void quitFromTray();

private:
    struct SyncTask {
        QString id;
        QString name;
        QString linkText;
        QString targetFolder;
        QString status;
        QString detail;
        QStringList ignoreRules;
        SyncLink link;
        bool linkReady = false;
        bool running = false;
        quint64 localBytes = 0;
        quint64 globalBytes = 0;
        quint64 receivedBytes = 0;
        quint64 sentBytes = 0;
        int localFiles = 0;
        int connectedDevices = 0;
        int totalDevices = 1;
    };

    void closeEvent(QCloseEvent* event) override;
    void buildUi();
    void setupTray();
    void loadTasks();
    void saveTasks() const;
    void refreshTaskTable();
    void refreshButtons();
    int selectedTaskIndex() const;
    SyncTask* selectedTask();
    const SyncTask* selectedTask() const;
    void setTaskStatus(const QString& taskID, const QString& status, const QString& detail = QString());
    bool parseTaskLink(SyncTask* task, QString* error);
    bool runTaskDialog(SyncTask* task, bool editing);
    void showTaskParameters(SyncTask* task);
    QString taskDiagnosticsText(const SyncTask& task) const;
    QString formatBytes(quint64 value) const;
    QString formatRate(quint64 value) const;
    void appendLog(const QString& message);
    QString diagnosticsText() const;

    QTableWidget* taskTable = nullptr;
    QPlainTextEdit* logEdit = nullptr;
    QPushButton* startButton = nullptr;
    QPushButton* pauseButton = nullptr;
    QPushButton* rescanButton = nullptr;
    QPushButton* parametersButton = nullptr;
    QPushButton* deleteButton = nullptr;
    QLabel* summaryLabel = nullptr;
    QList<SyncTask> tasks;
    QMap<QString, QThread*> connectionThreads;
    QSystemTrayIcon* trayIcon = nullptr;
    QMenu* trayMenu = nullptr;
    bool exiting = false;
    bool trayCloseTipShown = false;
};
