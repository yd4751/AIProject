"""
把签名图片从「嵌入式(inline)」改为「浮于文字上方(anchor)」，
让签名不占用行高、不把日期挤到第二页，同时恢复模板原有的 22pt 固定行距。
直接对 OOXML 做最小字符串替换，避免命名空间重写影响 Word 兼容性。
"""
import os
import re
import shutil
import zipfile

DOCX = r'E:\Project\workbuddy\WebView\output\互联网信息服务核准承诺书（个人版）-杨东.docx'
BAK = DOCX + '.inline.bak'
PARA_ID = '3E736DA9'

ANCHOR_ATTRS = ('distT="0" distB="0" distL="0" distR="0" simplePos="0" '
                'relativeHeight="251658240" behindDoc="0" locked="0" '
                'layoutInCell="1" allowOverlap="1"')


def main():
    z = zipfile.ZipFile(DOCX)
    items = [(info, z.read(info.filename)) for info in z.infolist()]
    z.close()

    xml = None
    for info, data in items:
        if info.filename == 'word/document.xml':
            xml = data.decode('utf-8')
            break
    if xml is None:
        raise RuntimeError('找不到 word/document.xml')

    m = re.search(r'(<w:p\b[^>]*' + PARA_ID + r'[^>]*>)(.*?)(</w:p>)', xml, re.S)
    if not m:
        raise RuntimeError('找不到签名段落')
    head, body, tail = m.group(1), m.group(2), m.group(3)

    # 1) 恢复模板原有行距（440 twips = 22pt, exact）
    body, n1 = re.subn(r'<w:spacing[^>]*/>',
                       '<w:spacing w:line="440" w:lineRule="exact"/>', body, count=1)
    if n1 != 1:
        raise RuntimeError('行距替换失败')

    # 2) inline -> anchor
    inline = re.search(r'<wp:inline\b[^>]*>(.*?)</wp:inline>', body, re.S)
    if not inline:
        raise RuntimeError('签名段落中没有 inline 图片')
    inner = inline.group(1)

    extent = re.search(r'<wp:extent\b[^>]*/>', inner).group(0)
    effect = re.search(r'<wp:effectExtent\b[^>]*/>', inner).group(0)
    docpr = re.search(r'<wp:docPr\b[^>]*/>', inner).group(0)
    graphic = re.search(r'<a:graphic>.*?</a:graphic>', inner, re.S).group(0)

    cy = int(re.search(r'cy="(\d+)"', extent).group(1))
    # 图片高 44.25pt，行高 22pt；上移一半差值使签名垂直居中于签名行
    offset = -int((cy / 12700 - 22) / 2 * 12700)

    anchor = (
        '<wp:anchor ' + ANCHOR_ATTRS + '>'
        '<wp:simplePos x="0" y="0"/>'
        '<wp:positionH relativeFrom="character"><wp:posOffset>0</wp:posOffset></wp:positionH>'
        '<wp:positionV relativeFrom="line"><wp:posOffset>' + str(offset) + '</wp:posOffset></wp:positionV>'
        + extent + effect +
        '<wp:wrapNone/>' + docpr +
        '<wp:cNvGraphicFramePr/>' + graphic +
        '</wp:anchor>'
    )
    body = body[:inline.start()] + anchor + body[inline.end():]

    new_xml = xml[:m.start()] + head + body + tail + xml[m.end():]

    if not os.path.exists(BAK):
        shutil.copy2(DOCX, BAK)
    with zipfile.ZipFile(DOCX, 'w') as out:
        for info, data in items:
            if info.filename == 'word/document.xml':
                data = new_xml.encode('utf-8')
            out.writestr(info, data)

    print('已把签名改为浮动图片，垂直偏移(EMU):', offset)
    print('已恢复行距: 22pt exact')
    print('备份:', BAK)


if __name__ == '__main__':
    main()
