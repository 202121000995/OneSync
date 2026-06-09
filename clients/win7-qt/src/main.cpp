#include "MainWindow.h"

#include <QApplication>

int main(int argc, char* argv[])
{
    QApplication app(argc, argv);
    QApplication::setApplicationName(QStringLiteral("OneSync Win7"));
    QApplication::setApplicationVersion(QStringLiteral("0.1"));
    QApplication::setOrganizationName(QStringLiteral("OneSync"));

    MainWindow window;
    window.show();
    return app.exec();
}
